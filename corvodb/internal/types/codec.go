package types

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Two encodings live here.
//
// AppendKey produces an order preserving byte string: comparing two encoded
// keys with bytes.Compare gives the same answer as comparing the values. That
// is what lets the B+Tree double as a SQL index.
//
// AppendRow is a compact, non ordered encoding used for row payloads.
const (
	keyNull = 0x00
	keyBool = 0x01
	keyInt  = 0x02
	keyReal = 0x03
	keyText = 0x04
)

// ErrBadEncoding is returned when a stored row cannot be decoded.
var ErrBadEncoding = errors.New("types: malformed encoding")

// AppendKey appends the order preserving encoding of v to dst.
func AppendKey(dst []byte, v Value) []byte {
	switch v.T {
	case Integer:
		dst = append(dst, keyInt)
		return binary.BigEndian.AppendUint64(dst, uint64(v.I)^(1<<63))
	case Float:
		dst = append(dst, keyReal)
		bits := math.Float64bits(v.F)
		if bits&(1<<63) != 0 {
			bits = ^bits // negative numbers reverse
		} else {
			bits |= 1 << 63
		}
		return binary.BigEndian.AppendUint64(dst, bits)
	case Text:
		dst = append(dst, keyText)
		// 0x00 is escaped so the terminator stays unambiguous, which keeps
		// prefixes of composite keys comparable.
		for i := 0; i < len(v.S); i++ {
			if v.S[i] == 0x00 {
				dst = append(dst, 0x00, 0xFF)
				continue
			}
			dst = append(dst, v.S[i])
		}
		return append(dst, 0x00, 0x00)
	case Boolean:
		dst = append(dst, keyBool)
		if v.B {
			return append(dst, 1)
		}
		return append(dst, 0)
	default:
		return append(dst, keyNull)
	}
}

// EncodeKey is AppendKey over a fresh slice.
func EncodeKey(v Value) []byte { return AppendKey(nil, v) }

// EncodeKeys encodes a composite key.
func EncodeKeys(values ...Value) []byte {
	var out []byte
	for _, v := range values {
		out = AppendKey(out, v)
	}
	return out
}

// AppendRow appends the storage encoding of a row to dst.
func AppendRow(dst []byte, row []Value) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(row)))
	for _, v := range row {
		dst = append(dst, byte(v.T))
		switch v.T {
		case Integer:
			dst = binary.AppendVarint(dst, v.I)
		case Float:
			dst = binary.LittleEndian.AppendUint64(dst, math.Float64bits(v.F))
		case Text:
			dst = binary.AppendUvarint(dst, uint64(len(v.S)))
			dst = append(dst, v.S...)
		case Boolean:
			if v.B {
				dst = append(dst, 1)
			} else {
				dst = append(dst, 0)
			}
		}
	}
	return dst
}

// EncodeRow is AppendRow over a fresh slice.
func EncodeRow(row []Value) []byte { return AppendRow(nil, row) }

// DecodeRow reads a row written by AppendRow.
func DecodeRow(src []byte) ([]Value, error) {
	count, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, ErrBadEncoding
	}
	src = src[n:]

	row := make([]Value, 0, count)
	for i := uint64(0); i < count; i++ {
		if len(src) == 0 {
			return nil, ErrBadEncoding
		}
		t := Type(src[0])
		src = src[1:]

		switch t {
		case Null:
			row = append(row, NullValue)
		case Integer:
			value, n := binary.Varint(src)
			if n <= 0 {
				return nil, ErrBadEncoding
			}
			src = src[n:]
			row = append(row, NewInt(value))
		case Float:
			if len(src) < 8 {
				return nil, ErrBadEncoding
			}
			row = append(row, NewFloat(math.Float64frombits(binary.LittleEndian.Uint64(src))))
			src = src[8:]
		case Text:
			length, n := binary.Uvarint(src)
			if n <= 0 || uint64(len(src[n:])) < length {
				return nil, ErrBadEncoding
			}
			src = src[n:]
			row = append(row, NewText(string(src[:length])))
			src = src[length:]
		case Boolean:
			if len(src) < 1 {
				return nil, ErrBadEncoding
			}
			row = append(row, NewBool(src[0] == 1))
			src = src[1:]
		default:
			return nil, fmt.Errorf("%w: unknown type tag %d", ErrBadEncoding, t)
		}
	}
	return row, nil
}

// DecodeKey reads one value written by AppendKey and returns the rest of the
// buffer, which is what makes composite index keys decodable.
func DecodeKey(src []byte) (Value, []byte, error) {
	if len(src) == 0 {
		return NullValue, nil, ErrBadEncoding
	}
	tag, src := src[0], src[1:]
	switch tag {
	case keyNull:
		return NullValue, src, nil
	case keyBool:
		if len(src) < 1 {
			return NullValue, nil, ErrBadEncoding
		}
		return NewBool(src[0] == 1), src[1:], nil
	case keyInt:
		if len(src) < 8 {
			return NullValue, nil, ErrBadEncoding
		}
		return NewInt(int64(binary.BigEndian.Uint64(src) ^ (1 << 63))), src[8:], nil
	case keyReal:
		if len(src) < 8 {
			return NullValue, nil, ErrBadEncoding
		}
		bits := binary.BigEndian.Uint64(src)
		if bits&(1<<63) != 0 {
			bits &^= 1 << 63
		} else {
			bits = ^bits
		}
		return NewFloat(math.Float64frombits(bits)), src[8:], nil
	case keyText:
		var out []byte
		for i := 0; i < len(src); i++ {
			if src[i] != 0x00 {
				out = append(out, src[i])
				continue
			}
			if i+1 >= len(src) {
				return NullValue, nil, ErrBadEncoding
			}
			switch src[i+1] {
			case 0x00:
				return NewText(string(out)), src[i+2:], nil
			case 0xFF:
				out = append(out, 0x00)
				i++
			default:
				return NullValue, nil, ErrBadEncoding
			}
		}
		return NullValue, nil, ErrBadEncoding
	default:
		return NullValue, nil, fmt.Errorf("%w: unknown key tag %d", ErrBadEncoding, tag)
	}
}
