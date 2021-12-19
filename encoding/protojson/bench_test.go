// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protojson_test

import (
	"google.golang.org/protobuf/types/known/durationpb"
	"math"
	"testing"

	pb3 "google.golang.org/protobuf/internal/testprotos/textpb3"

	"google.golang.org/protobuf/encoding/protojson"
)

func BenchmarkUnmarshal_Duration(b *testing.B) {
	input := []byte(`"-123456789.123456789s"`)

	for i := 0; i < b.N; i++ {
		err := protojson.Unmarshal(input, &durationpb.Duration{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

var scalarSample = pb3.Scalars{
	SBool:     true,
	SInt32:    math.MaxInt32,
	SInt64:    math.MaxInt64,
	SUint64:   math.MaxUint64,
	SUint32:   2130321213,
	SSint32:   23132131,
	SSint64:   3213232123812,
	SFixed32:  4232943192,
	SFixed64:  2389182192312,
	SSfixed32: 281432823,
	SSfixed64: 23919321921,
	SFloat:    3213213.2931,
	SDouble:   39121.321321,
	SBytes:    []byte("foo_sbytes"),
	SString:   "foo_sstring",
}

func BenchmarkMarshal_ScalarsUnordered(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := protojson.MarshalOptions{Unordered: true}.Marshal(&scalarSample)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshal_Scalars(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, err := protojson.Marshal(&scalarSample)
		if err != nil {
			b.Fatal(err)
		}
	}
}
