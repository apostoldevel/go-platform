package frame

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParse_Call(t *testing.T) {
	f, err := Parse([]byte(`{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","a":"/register","p":{"module":"clients"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.Type != Call || f.Action != "/register" || f.ID != "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11" {
		t.Fatalf("got %+v", f)
	}
	var p struct{ Module string }
	if err := json.Unmarshal(f.Payload, &p); err != nil || p.Module != "clients" {
		t.Fatalf("payload: %v %+v", err, p)
	}
}

func TestParse_PayloadAbsentIsEmptyObject(t *testing.T) {
	f, err := Parse([]byte(`{"t":3,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Payload) != "{}" {
		t.Fatalf("payload %q, want {}", f.Payload)
	}
}

func TestParse_Rejects(t *testing.T) {
	cases := map[string]string{
		"not json":        `{`,
		"no u":            `{"t":2,"a":"/x"}`,
		"bad u":           `{"t":2,"u":"nope","a":"/x"}`,
		"type 0 open":     `{"t":0,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}`,
		"type 1 close":    `{"t":1,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}`,
		"type 9":          `{"t":9,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}`,
		"call without a":  `{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}`,
		"a without /":     `{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","a":"register"}`,
		"p not object":    `{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","a":"/x","p":[1]}`,
		"error without c": `{"t":4,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","m":"x"}`,
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted %s", name, in)
		}
	}
}

func TestParse_TooLarge(t *testing.T) {
	big := `{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","a":"/x","p":{"x":"` + strings.Repeat("a", MaxSize) + `"}}`
	if _, err := Parse([]byte(big)); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestEncode_RoundTrip(t *testing.T) {
	in := Frame{Type: CallError, ID: "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11", Code: 422, Message: "bad prefix"}
	b, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != CallError || out.Code != 422 || out.Message != "bad prefix" || out.ID != in.ID {
		t.Fatalf("round trip: %+v", out)
	}
	if strings.Contains(string(b), `"a"`) || strings.Contains(string(b), `"p"`) {
		t.Fatalf("CALLERROR must not carry a or p: %s", b)
	}
}

func TestNewCall_GeneratesID(t *testing.T) {
	f := NewCall("/heartbeat", map[string]any{"in_flight": 3})
	if f.Type != Call || f.Action != "/heartbeat" || len(f.ID) != 36 {
		t.Fatalf("%+v", f)
	}
	b, _ := f.Encode()
	if !strings.Contains(string(b), `"in_flight":3`) {
		t.Fatalf("payload not encoded: %s", b)
	}
}
