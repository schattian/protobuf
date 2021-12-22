// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package json

import (
	"bytes"
	"encoding/base64"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/schattian/protobuf/internal/detrand"
	"github.com/schattian/protobuf/internal/errors"
)

// kind represents an encoding type.
type kind uint8

var encoderPool sync.Pool

const (
	_ kind = (1 << iota) / 2
	name
	scalar
	objectOpen
	objectClose
	arrayOpen
	arrayClose
)

// Encoder provides methods to write out JSON constructs and values. The user is
// responsible for producing valid sequences of JSON constructs and values.
type Encoder struct {
	indent   string
	lastKind kind
	indents  []byte
	out      bytes.Buffer
	scratch  [64]byte

	ptrLevel uint
	ptrSeen  map[interface{}]struct{}
}

func (e *Encoder) Reset() {
	e.out.Reset()
	e.lastKind = 0
}

func (e *Encoder) SetIndent(indent string) error {
	if len(indent) == 0 {
		e.indent = ""
		return nil
	}
	if strings.Trim(indent, " \t") != "" {
		return errors.New("indent may only be composed of space or tab characters")
	}
	e.indent = indent
	return nil
}

// NewEncoder returns an Encoder.
//
// If indent is a non-empty string, it causes every entry for an Array or Object
// to be preceded by the indent and trailed by a newline.
func NewEncoder() *Encoder {
	return &Encoder{}
}

// Bytes returns the content of the written bytes.
func (e *Encoder) Bytes() []byte {
	return e.out.Bytes()
}

// WriteNull writes out the null value.
func (e *Encoder) WriteNull() {
	e.prepareNext(scalar)
	e.out.WriteString("null")
}

// WriteBool writes out the given boolean value.
func (e *Encoder) WriteBool(b bool) {
	e.prepareNext(scalar)
	if b {
		e.out.WriteString("true")
	} else {
		e.out.WriteString("false")
	}
}

// WriteString writes out the given string in JSON string value. Returns error
// if input string contains invalid UTF-8.
func (e *Encoder) WriteString(s string) error {
	e.prepareNext(scalar)
	var err error
	if err = e.appendString(s); err != nil {
		return err
	}
	return nil
}

func (e *Encoder) WriteByteSlice(s []byte) {
	e.prepareNext(scalar)

	e.out.WriteByte('"')
	encodedLen := base64.StdEncoding.EncodedLen(len(s))
	if encodedLen <= len(e.scratch) {
		// If the encoded bytes fit in e.scratch, avoid an extra
		// allocation and use the cheaper Encoding.Encode.
		dst := e.scratch[:encodedLen]
		base64.StdEncoding.Encode(dst, s)
		e.out.Write(dst)
	} else if encodedLen <= 1024 {
		// The encoded bytes are short enough to allocate for, and
		// Encoding.Encode is still cheaper.
		dst := make([]byte, encodedLen)
		base64.StdEncoding.Encode(dst, s)
		e.out.Write(dst)
	} else {
		// The encoded bytes are too long to cheaply allocate, and
		// Encoding.Encode is no longer noticeably cheaper.
		enc := base64.NewEncoder(base64.StdEncoding, &e.out)
		enc.Write(s)
		enc.Close()
	}
	e.out.WriteByte('"')
}

// Sentinel error used for indicating invalid UTF-8.
var errInvalidUTF8 = errors.New("invalid UTF-8")

func (e *Encoder) appendString(in string) error {
	e.out.WriteByte('"')
	i := indexNeedEscapeInString(in)
	e.out.WriteString(in[:i])
	in = in[i:]
	for len(in) > 0 {
		switch r, n := utf8.DecodeRuneInString(in); {
		case r == utf8.RuneError && n == 1:
			return errInvalidUTF8
		case r < ' ' || r == '"' || r == '\\':
			e.out.WriteByte('\\')
			switch r {
			case '"', '\\':
				e.out.WriteRune(r)
			case '\b':
				e.out.WriteByte('b')
			case '\f':
				e.out.WriteByte('f')
			case '\n':
				e.out.WriteByte('n')
			case '\r':
				e.out.WriteByte('r')
			case '\t':
				e.out.WriteByte('t')
			default:
				e.out.WriteByte('u')
				e.out.WriteString("0000"[1+(bits.Len32(uint32(r))-1)/4:])
				b := strconv.AppendUint(e.scratch[:0], uint64(r), 16)
				e.out.Write(b)
			}
			in = in[n:]
		default:
			i := indexNeedEscapeInString(in[n:])
			e.out.WriteString(in[:n+i])
			in = in[n+i:]
		}
	}
	e.out.WriteByte('"')
	return nil
}

// indexNeedEscapeInString returns the index of the character that needs
// escaping. If no characters need escaping, this returns the input length.
func indexNeedEscapeInString(s string) int {
	for i, r := range s {
		if r < ' ' || r == '\\' || r == '"' || r == utf8.RuneError {
			return i
		}
	}
	return len(s)
}

// WriteFloat writes out the given float and bitSize in JSON number value.
func (e *Encoder) WriteFloat(n float64, bitSize int) {
	e.prepareNext(scalar)
	e.appendFloat(n, bitSize)
}

func (e *Encoder) appendFloat(n float64, bitSize int) {
	switch {
	case math.IsNaN(n):
		e.out.WriteString(`"NaN"`)
		return
	case math.IsInf(n, +1):
		e.out.WriteString(`"Infinity"`)
		return
	case math.IsInf(n, -1):
		e.out.WriteString(`"-Infinity"`)
		return
	}

	// JSON number formatting logic based on encoding/json.
	// See floatEncoder.encode for reference.
	b := e.scratch[:0]
	fmt := byte('f')
	if abs := math.Abs(n); abs != 0 {
		if bitSize == 64 && (abs < 1e-6 || abs >= 1e21) ||
			bitSize == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			fmt = 'e'
		}
	}
	b = strconv.AppendFloat(b, n, fmt, -1, bitSize)
	if fmt == 'e' {
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}

	e.out.Write(b)
}

// WriteInt writes out the given signed integer in JSON number value.
func (e *Encoder) WriteInt(n int64) {
	e.prepareNext(scalar)
	b := strconv.AppendInt(e.scratch[:0], n, 10)
	e.out.Write(b)
}

// WriteUint writes out the given unsigned integer in JSON number value.
func (e *Encoder) WriteUint(n uint64) {
	e.prepareNext(scalar)
	b := strconv.AppendUint(e.scratch[:0], n, 10)
	e.out.Write(b)
}

// WriteInt64 writes the given int64 as string (as it's specified by the I-JSON spec)
func (e *Encoder) WriteInt64(n int64) error {
	e.prepareNext(scalar)
	b := strconv.AppendInt(e.scratch[:0], n, 10)
	e.out.WriteByte('"')
	e.out.Write(b)
	e.out.WriteByte('"')
	return nil
}

// WriteUint64 writes the given uint64 as string (as it's specified by the I-JSON spec)
func (e *Encoder) WriteUint64(n uint64) error {
	e.prepareNext(scalar)
	b := strconv.AppendUint(e.scratch[:0], n, 10)
	e.out.WriteByte('"')
	e.out.Write(b)
	e.out.WriteByte('"')
	return nil
}

// StartObject writes out the '{' symbol.
func (e *Encoder) StartObject() {
	e.prepareNext(objectOpen)
	e.out.WriteByte('{')
}

// EndObject writes out the '}' symbol.
func (e *Encoder) EndObject() {
	e.prepareNext(objectClose)
	e.out.WriteByte('}')
}

// WriteName writes out the given string in JSON string value and the name
// separator ':'. Returns error if input string contains invalid UTF-8, which
// should not be likely as protobuf field names should be valid.
func (e *Encoder) WriteName(s string) error {
	e.prepareNext(name)
	var err error
	// Append to output regardless of error.
	err = e.appendString(s)
	e.out.WriteByte(':')
	return err
}

// StartArray writes out the '[' symbol.
func (e *Encoder) StartArray() {
	e.prepareNext(arrayOpen)
	e.out.WriteByte('[')
}

// EndArray writes out the ']' symbol.
func (e *Encoder) EndArray() {
	e.prepareNext(arrayClose)
	e.out.WriteByte(']')
}

// prepareNext adds possible comma and indentation for the next value based
// on last type and indent option. It also updates lastKind to next.
func (e *Encoder) prepareNext(next kind) {
	defer func() {
		// Set lastKind to next.
		e.lastKind = next
	}()

	if len(e.indent) == 0 {
		// Need to add comma on the following condition.
		if e.lastKind&(scalar|objectClose|arrayClose) != 0 &&
			next&(name|scalar|objectOpen|arrayOpen) != 0 {
			e.out.WriteByte(',')
			// For single-line output, add a random extra space after each
			// comma to make output unstable.
			if detrand.Bool() {
				e.out.WriteByte(' ')
			}
		}
		return
	}

	switch {
	case e.lastKind&(objectOpen|arrayOpen) != 0:
		// If next type is NOT closing, add indent and newline.
		if next&(objectClose|arrayClose) == 0 {
			e.indents = append(e.indents, e.indent...)
			e.out.WriteByte('\n')
			e.out.Write(e.indents)
		}

	case e.lastKind&(scalar|objectClose|arrayClose) != 0:
		switch {
		// If next type is either a value or name, add comma and newline.
		case next&(name|scalar|objectOpen|arrayOpen) != 0:
			e.out.WriteByte(',')
			e.out.WriteByte('\n')

		// If next type is a closing object or array, adjust indentation.
		case next&(objectClose|arrayClose) != 0:
			e.indents = e.indents[:len(e.indents)-len(e.indent)]
			e.out.WriteByte('\n')
		}
		e.out.Write(e.indents)

	case e.lastKind&name != 0:
		e.out.WriteByte(' ')
		// For multi-line output, add a random extra space after key: to make
		// output unstable.
		if detrand.Bool() {
			e.out.WriteByte(' ')
		}
	}
}
