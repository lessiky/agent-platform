package model

import (
	"testing"
)

func TestVectorValueScanRoundTrip(t *testing.T) {
	in := Vector{V: []float32{0.1, -0.2, 3.14159, 0, 1e-7, 123456.5}}
	val, err := in.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("Value type = %T, want string", val)
	}
	if s == "" || s[0] != '[' || s[len(s)-1] != ']' {
		t.Fatalf("Value format = %q, want [..]", s)
	}
	var out Vector
	if err := out.Scan(s); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(out.V) != len(in.V) {
		t.Fatalf("len = %d, want %d", len(out.V), len(in.V))
	}
	for i := range in.V {
		if out.V[i] != in.V[i] {
			t.Fatalf("V[%d] = %v, want %v", i, out.V[i], in.V[i])
		}
	}
}

func TestVectorScanNil(t *testing.T) {
	var v Vector
	if err := v.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if !v.IsNull() {
		t.Fatal("nil scan should be null")
	}
	// 指针 NULL 语义
	var p *Vector
	if p == nil && !p.IsNull() {
		t.Fatal("nil pointer should be null")
	}
}

func TestVectorScanByteSliceAndEmpty(t *testing.T) {
	var v Vector
	if err := v.Scan([]byte("[1,2,3]")); err != nil {
		t.Fatalf("Scan([]byte): %v", err)
	}
	if len(v.V) != 3 || v.V[0] != 1 || v.V[2] != 3 {
		t.Fatalf("V = %v", v.V)
	}
	var v2 Vector
	if err := v2.Scan("[]"); err != nil {
		t.Fatalf("Scan([]): %v", err)
	}
	if !v2.IsNull() {
		t.Fatal("[] should be null")
	}
}

func TestVectorValueEmpty(t *testing.T) {
	val, err := Vector{}.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if val != nil {
		t.Fatalf("empty Value should be nil (NULL), got %v", val)
	}
}
