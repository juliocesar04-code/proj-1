// Package types holds the value model shared by the parser and the execution
// engine: the supported SQL types, comparison rules, and the encodings used to
// store rows and index keys.
package types

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Type is the declared type of a column.
type Type uint8

// Supported column types.
const (
	Null Type = iota
	Integer
	Float
	Text
	Boolean
)

func (t Type) String() string {
	switch t {
	case Integer:
		return "INTEGER"
	case Float:
		return "FLOAT"
	case Text:
		return "TEXT"
	case Boolean:
		return "BOOLEAN"
	default:
		return "NULL"
	}
}

// ParseType maps a SQL type name onto a Type.
func ParseType(name string) (Type, bool) {
	switch strings.ToUpper(name) {
	case "INT", "INTEGER", "BIGINT", "SMALLINT":
		return Integer, true
	case "FLOAT", "REAL", "DOUBLE", "DECIMAL", "NUMERIC":
		return Float, true
	case "TEXT", "VARCHAR", "CHAR", "STRING":
		return Text, true
	case "BOOL", "BOOLEAN":
		return Boolean, true
	default:
		return Null, false
	}
}

// Value is a single SQL value. The zero value is NULL.
type Value struct {
	T Type
	I int64
	F float64
	S string
	B bool
}

// Constructors.
var NullValue = Value{}

func NewInt(i int64) Value     { return Value{T: Integer, I: i} }
func NewFloat(f float64) Value { return Value{T: Float, F: f} }
func NewText(s string) Value   { return Value{T: Text, S: s} }
func NewBool(b bool) Value     { return Value{T: Boolean, B: b} }

// IsNull reports whether the value is SQL NULL.
func (v Value) IsNull() bool { return v.T == Null }

// Float64 returns the numeric value of an INTEGER or FLOAT.
func (v Value) Float64() (float64, bool) {
	switch v.T {
	case Integer:
		return float64(v.I), true
	case Float:
		return v.F, true
	default:
		return 0, false
	}
}

// String renders the value the way the shell prints it.
func (v Value) String() string {
	switch v.T {
	case Integer:
		return strconv.FormatInt(v.I, 10)
	case Float:
		return strconv.FormatFloat(v.F, 'g', -1, 64)
	case Text:
		return v.S
	case Boolean:
		if v.B {
			return "true"
		}
		return "false"
	default:
		return "NULL"
	}
}

// Truthy implements the three valued logic of SQL conditions: only a boolean
// true passes a WHERE clause.
func (v Value) Truthy() bool { return v.T == Boolean && v.B }

// Equal reports value equality, with NULL equal only to NULL.
func (v Value) Equal(o Value) bool {
	cmp, ok := Compare(v, o)
	return ok && cmp == 0
}

// Compare orders two values. It reports ok=false when the pair has no defined
// order, which happens for mixed non numeric types.
func Compare(a, b Value) (int, bool) {
	if a.T == Null || b.T == Null {
		return 0, a.T == Null && b.T == Null
	}
	if af, ok := a.Float64(); ok {
		bf, ok := b.Float64()
		if !ok {
			return 0, false
		}
		if a.T == Integer && b.T == Integer {
			return cmp(a.I, b.I), true
		}
		if math.IsNaN(af) || math.IsNaN(bf) {
			return 0, false
		}
		return cmp(af, bf), true
	}
	if a.T != b.T {
		return 0, false
	}
	switch a.T {
	case Text:
		return strings.Compare(a.S, b.S), true
	case Boolean:
		return cmp(boolToInt(a.B), boolToInt(b.B)), true
	}
	return 0, false
}

func cmp[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Cast converts a value to the declared type of a column. Widening an integer
// to a float is allowed; anything else that does not already match is an
// error.
func Cast(v Value, t Type) (Value, error) {
	if v.IsNull() || v.T == t {
		return v, nil
	}
	if t == Float && v.T == Integer {
		return NewFloat(float64(v.I)), nil
	}
	return v, fmt.Errorf("cannot store %s into a %s column", v.T, t)
}
