// Package numeric carries a value of an SQL numeric parameter with every
// digit the client wrote. A float64 holds about 15 significant digits: an
// amount decoded into one is rounded before the database sees it, where
// numeric takes every digit. A Value keeps the digits of the JSON number as
// plain decimal text (an exponent is folded into the digits by string
// arithmetic, never through a float), encodes as that number and reaches the
// database as text, which numeric reads without loss. The text never has an
// exponent: pgx cannot encode one into numeric's binary parameter.
package numeric

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
)

// maxExponent caps the magnitude of an exponent: a larger one is refused
// before any digit is allocated.
const maxExponent = 1000

// Value is the plain decimal text (no exponent) of a JSON number. The zero Value is not a number: it is
// what a Value holds before it is decoded, and encoding it is an error.
type Value string

// UnmarshalJSON takes a JSON number and nothing else: a string (even one
// holding digits), a bool, an array or an object is the decoder's type
// error, as it is for a float64. A literal null leaves the Value as it is,
// as encoding/json does for the types it knows; a pointer field is set to
// nil by the decoder before this is called. An exponent is folded into the
// digits ("1.5E2" is "150", "2e-3" is "0.002"); an exponent whose magnitude
// exceeds 1000 is the decoder's type error.
func (v *Value) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) == 0 || (b[0] != '-' && (b[0] < '0' || b[0] > '9')) {
		return &json.UnmarshalTypeError{Value: kindOf(b), Type: reflect.TypeFor[Value]()}
	}
	// the decoder hands a method only a well-formed JSON value, so a token
	// starting with a digit or '-' is a valid JSON number
	text, ok := plain(string(b))
	if !ok {
		return &json.UnmarshalTypeError{Value: "number", Type: reflect.TypeFor[Value]()}
	}
	*v = Value(text)
	return nil
}

// plain rewrites a valid JSON number as decimal text without an exponent.
// It reports false for an exponent beyond maxExponent.
func plain(s string) (string, bool) {
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(strings.TrimPrefix(s[i+1:], "+"))
		if err != nil || e > maxExponent || e < -maxExponent {
			return "", false
		}
		exp, s = e, s[:i]
	}
	if exp == 0 {
		return sign + s, true
	}
	intPart, frac, _ := strings.Cut(s, ".")
	digits := intPart + frac
	point := len(intPart) + exp // where the point falls in digits
	switch {
	case point <= 0:
		intPart, frac = "0", strings.Repeat("0", -point)+digits
	case point >= len(digits):
		intPart, frac = digits+strings.Repeat("0", point-len(digits)), ""
	default:
		intPart, frac = digits[:point], digits[point:]
	}
	if t := strings.TrimLeft(intPart, "0"); t == "" {
		intPart = "0"
	} else {
		intPart = t
	}
	if frac != "" {
		intPart += "." + frac
	}
	return sign + intPart, true
}

// kindOf names the JSON kind of a token for the type error.
func kindOf(b []byte) string {
	if len(b) == 0 {
		return "empty"
	}
	switch b[0] {
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case '[':
		return "array"
	case '{':
		return "object"
	}
	return "value"
}

var errEmpty = errors.New("numeric: an empty Value is not a number")

// MarshalJSON is the number as decimal text.
func (v Value) MarshalJSON() ([]byte, error) {
	if v == "" {
		return nil, errEmpty
	}
	return []byte(v), nil
}

// Value is the text numeric reads: a direct call passes it as text, and the
// parameter's type makes it a numeric on the server.
func (v Value) Value() (driver.Value, error) {
	if v == "" {
		return nil, errEmpty
	}
	return string(v), nil
}

// Sign is -1, 0 or 1 by the number's sign, read from its digits: "-0",
// "0.000" and "0e5" are zero. It is for a check of the sign before the
// call; the value itself is never converted.
func (v Value) Sign() int {
	s := string(v)
	neg := strings.HasPrefix(s, "-")
	mant := strings.TrimPrefix(s, "-")
	if i := strings.IndexAny(mant, "eE"); i >= 0 {
		mant = mant[:i]
	}
	if strings.Trim(mant, "0.") == "" {
		return 0
	}
	if neg {
		return -1
	}
	return 1
}
