package pgtx

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCallSQL_NamedArgumentsInKeyOrder(t *testing.T) {
	sql, vals, err := callSQL("api.list_user", Args{"search": json.RawMessage(`[]`), "limit": 5, "orderby": nil})
	if err != nil {
		t.Fatal(err)
	}
	if want := "SELECT to_json(t) FROM api.list_user(plimit => $1, porderby => $2, psearch => $3) t"; sql != want {
		t.Fatalf("sql:\n %s\nwant\n %s", sql, want)
	}
	if !reflect.DeepEqual(vals, []any{5, nil, json.RawMessage(`[]`)}) {
		t.Fatalf("vals %#v", vals)
	}
}

func TestCallSQL_BareNameAndNoArguments(t *testing.T) {
	sql, vals, err := callSQL("current_userid", nil)
	if err != nil || sql != "SELECT to_json(t) FROM api.current_userid() t" || len(vals) != 0 {
		t.Fatalf("%q %v %v", sql, vals, err)
	}
}

func TestCallSQL_TypedPinsTheOverload(t *testing.T) {
	sql, vals, err := callSQL("set_session_area", Args{"area": Typed{V: "7f", Type: "uuid"}})
	if err != nil || sql != "SELECT to_json(t) FROM api.set_session_area(parea => $1::uuid) t" || !reflect.DeepEqual(vals, []any{"7f"}) {
		t.Fatalf("%q %v %v", sql, vals, err)
	}
	if _, _, err := callSQL("f", Args{"a": Typed{V: 1, Type: "int); DROP TABLE x; --"}}); err == nil {
		t.Fatal("a type outside the identifier grammar must be refused")
	}
}

func TestCallSQL_RefusesWhatIsNotAnIdentifier(t *testing.T) {
	for _, fn := range []string{"", "api.", "kernel.f", "api.f;", "f(1)", "F", "api.list_user t --", "db.user"} {
		if _, _, err := callSQL(fn, nil); err == nil {
			t.Errorf("function %q must be refused", fn)
		}
	}
	for _, k := range []string{"", "Id", "id;", "a b", "1a"} {
		if _, _, err := callSQL("f", Args{k: 1}); err == nil {
			t.Errorf("key %q must be refused", k)
		}
	}
}

// The body daemon.call will take: keys as they are, JSON values embedded,
// a typed value by its value.
func TestArgs_JSON(t *testing.T) {
	ts := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	b, err := Args{"search": json.RawMessage(`[{"field":"x"}]`), "id": "7f", "none": nil, "at": ts, "area": Typed{V: "a1", Type: "text"}}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if s, ok := got["search"].([]any); !ok || len(s) != 1 {
		t.Fatalf("search must be embedded JSON, not a string: %s", b)
	}
	if got["id"] != "7f" || got["area"] != "a1" || got["at"] != "2026-09-26T10:00:00Z" {
		t.Fatalf("%s", b)
	}
	if v, has := got["none"]; !has || v != nil {
		t.Fatalf("nil must stay an explicit null: %s", b)
	}
}

func TestCallSQL_RefusesBareBytes(t *testing.T) {
	if _, _, err := callSQL("f", Args{"search": []byte(`[]`)}); err == nil {
		t.Fatal("[]byte must be refused: daemon.call would see base64")
	}
	if JSON(nil) != nil || JSON([]byte{}) != nil {
		t.Fatal("empty JSON is an explicit NULL")
	}
	if v, ok := JSON([]byte(`[1]`)).(json.RawMessage); !ok || string(v) != "[1]" {
		t.Fatal("JSON must be json.RawMessage")
	}
}
