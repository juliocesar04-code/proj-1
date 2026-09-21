package types

import (
	"bytes"
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestKeyEncodingPreservesOrder(t *testing.T) {
	values := []Value{
		NewInt(math.MinInt64), NewInt(-1000), NewInt(-1), NewInt(0), NewInt(1),
		NewInt(1000), NewInt(math.MaxInt64),
	}
	for i := 1; i < len(values); i++ {
		a, b := EncodeKey(values[i-1]), EncodeKey(values[i])
		if bytes.Compare(a, b) >= 0 {
			t.Fatalf("integer order broken between %v and %v", values[i-1], values[i])
		}
	}

	floats := []Value{
		NewFloat(math.Inf(-1)), NewFloat(-1e9), NewFloat(-0.5), NewFloat(0),
		NewFloat(0.5), NewFloat(1e9), NewFloat(math.Inf(1)),
	}
	for i := 1; i < len(floats); i++ {
		if bytes.Compare(EncodeKey(floats[i-1]), EncodeKey(floats[i])) >= 0 {
			t.Fatalf("float order broken between %v and %v", floats[i-1], floats[i])
		}
	}

	texts := []Value{NewText(""), NewText("a"), NewText("ab"), NewText("b"), NewText("\x00z")}
	sorted := append([]Value(nil), texts...)
	sort.Slice(sorted, func(i, j int) bool {
		c, _ := Compare(sorted[i], sorted[j])
		return c < 0
	})
	for i := 1; i < len(sorted); i++ {
		if bytes.Compare(EncodeKey(sorted[i-1]), EncodeKey(sorted[i])) >= 0 {
			t.Fatalf("text order broken between %q and %q", sorted[i-1].S, sorted[i].S)
		}
	}
}

func TestCompositeKeyPrefixesAreUnambiguous(t *testing.T) {
	// "a" followed by "b" must not collide with "ab" followed by anything.
	first := EncodeKeys(NewText("a"), NewText("b"))
	second := EncodeKeys(NewText("ab"), NewText(""))
	if bytes.Equal(first, second) {
		t.Fatal("composite keys collided")
	}
	prefix := EncodeKey(NewText("a"))
	if !bytes.HasPrefix(first, prefix) {
		t.Fatal("prefix scan would miss the entry")
	}
	if bytes.HasPrefix(second, prefix) {
		t.Fatal("prefix scan would return a foreign entry")
	}
}

func TestRowRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		row := make([]Value, rng.Intn(8))
		for j := range row {
			switch rng.Intn(5) {
			case 0:
				row[j] = NullValue
			case 1:
				row[j] = NewInt(rng.Int63() - rng.Int63())
			case 2:
				row[j] = NewFloat(rng.NormFloat64())
			case 3:
				row[j] = NewText(string(make([]byte, rng.Intn(40))))
			case 4:
				row[j] = NewBool(rng.Intn(2) == 0)
			}
		}
		decoded, err := DecodeRow(EncodeRow(row))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(decoded) != len(row) {
			t.Fatalf("row length changed: %d -> %d", len(row), len(decoded))
		}
		for j := range row {
			if decoded[j] != row[j] {
				t.Fatalf("value %d changed: %#v -> %#v", j, row[j], decoded[j])
			}
		}
	}
}

func TestDecodeRowRejectsGarbage(t *testing.T) {
	if _, err := DecodeRow([]byte{0x05, 0xFF}); err == nil {
		t.Fatal("expected an error for a truncated row")
	}
}

func TestKeyRoundTrip(t *testing.T) {
	values := []Value{
		NullValue, NewBool(true), NewBool(false), NewInt(-42), NewInt(0),
		NewInt(1 << 40), NewFloat(-3.5), NewFloat(0), NewFloat(2.25),
		NewText(""), NewText("hello"), NewText("with\x00zero"),
	}
	for _, want := range values {
		got, rest, err := DecodeKey(EncodeKey(want))
		if err != nil {
			t.Fatalf("decode %v: %v", want, err)
		}
		if len(rest) != 0 {
			t.Fatalf("decode %v left %d bytes behind", want, len(rest))
		}
		if got != want {
			t.Fatalf("round trip changed %#v into %#v", want, got)
		}
	}

	composite := EncodeKeys(NewText("a\x00b"), NewInt(7))
	first, rest, err := DecodeKey(composite)
	if err != nil {
		t.Fatal(err)
	}
	second, rest, err := DecodeKey(rest)
	if err != nil {
		t.Fatal(err)
	}
	if first.S != "a\x00b" || second.I != 7 || len(rest) != 0 {
		t.Fatalf("composite decode failed: %v %v", first, second)
	}
}
