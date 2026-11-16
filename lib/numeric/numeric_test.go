package numeric

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestExponentIsFolded(t *testing.T) {
	for in, want := range map[string]string{
		"1e3": "1000", "1.5E2": "150", "2e-3": "0.002", "-1.25e1": "-12.5",
		"0e5": "0", "1E+2": "100", "123e-2": "1.23", "1.50e1": "15.0",
		"0.5e1": "5", "0.05e-1": "0.005", "10e-1": "1.0",
		"1e400": "1" + strings.Repeat("0", 400), "1e1000": "1" + strings.Repeat("0", 1000),
	} {
		var v Value
		if err := json.Unmarshal([]byte(in), &v); err != nil || string(v) != want {
			t.Fatalf("%s: got %q, %v; want %q", in, v, err, want)
		}
		out, err := json.Marshal(v)
		if err != nil || string(out) != want {
			t.Fatalf("%s: encoded %s, %v", in, out, err)
		}
		dv, err := v.Value()
		if err != nil || dv != want {
			t.Fatalf("%s: driver value %v, %v", in, dv, err)
		}
	}
	if len(Value("1"+strings.Repeat("0", 400))) != 401 {
		t.Fatal("1e400 length")
	}
}

func TestExponentBeyondCap(t *testing.T) {
	for _, in := range []string{"1e1001", "1e-1001", "1e999999", "1E+99999999999999999999"} {
		var v Value
		err := json.Unmarshal([]byte(in), &v)
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			t.Fatalf("%s: want a type error, got %v", in, err)
		}
	}
}

// pgx cannot turn an exponent form into numeric's binary parameter ("cannot
// find encode plan" / "is not a number"); the folded text must.
func TestPgxEncodesFolded(t *testing.T) {
	m := pgtype.NewMap()
	for _, in := range []string{"1e3", "1.5E2", "2e-3", "-1.25e1", "0e5", "1e400", "12.5"} {
		var v Value
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatal(err)
		}
		dv, _ := v.Value()
		if _, err := m.Encode(pgtype.NumericOID, pgtype.BinaryFormatCode, dv, nil); err == nil {
			t.Fatalf("%s: a string encodes in binary now: revisit this test", in)
		}
		var n pgtype.Numeric
		if err := n.Scan(dv); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if _, err := m.Encode(pgtype.NumericOID, pgtype.BinaryFormatCode, n, nil); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
	}
}

func TestDecodeKeepsDigits(t *testing.T) {
	for _, in := range []string{"12.345678901234567890123", "-0.01", "0", "100000000000000000000000000.5"} {
		var s struct {
			X *Value `json:"x"`
		}
		if err := json.Unmarshal([]byte(`{"x":`+in+`}`), &s); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if s.X == nil || string(*s.X) != in {
			t.Fatalf("%s: decoded %v", in, s.X)
		}
		out, err := json.Marshal(s)
		if err != nil || string(out) != `{"x":`+in+`}` {
			t.Fatalf("%s: encoded %s, %v", in, out, err)
		}
		dv, err := s.X.Value()
		if err != nil || dv != in {
			t.Fatalf("%s: driver value %v, %v", in, dv, err)
		}
	}
}

func TestDecodeRefusesNonNumbers(t *testing.T) {
	for _, in := range []string{`"12"`, `"abc"`, `true`, `[1]`, `{"a":1}`} {
		var s struct {
			X *Value `json:"x"`
		}
		err := json.Unmarshal([]byte(`{"x":`+in+`}`), &s)
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			t.Fatalf("%s: want a type error, got %v", in, err)
		}
	}
}

func TestNull(t *testing.T) {
	var s struct {
		X *Value `json:"x"`
	}
	if err := json.Unmarshal([]byte(`{"x":null}`), &s); err != nil || s.X != nil {
		t.Fatalf("null: %v, %v", s.X, err)
	}
	v := Value("5")
	if err := v.UnmarshalJSON([]byte("null")); err != nil || v != "5" {
		t.Fatalf("null on a value: %q, %v", v, err)
	}
}

func TestEmptyIsNotANumber(t *testing.T) {
	if _, err := json.Marshal(Value("")); err == nil {
		t.Fatal("an empty Value encoded")
	}
	if _, err := Value("").Value(); err == nil {
		t.Fatal("an empty Value reached the driver")
	}
}

func TestSign(t *testing.T) {
	for in, want := range map[string]int{
		"0": 0, "-0": 0, "0.000": 0, "0e5": 0, "-0.0E-3": 0,
		"1": 1, "0.01": 1, "1e-400": 1, "12.5": 1,
		"-1": -1, "-0.001": -1, "-3E2": -1,
	} {
		if got := Value(in).Sign(); got != want {
			t.Errorf("Sign(%s) = %d, want %d", in, got, want)
		}
	}
}
