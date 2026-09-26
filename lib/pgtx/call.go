package pgtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Args are the named arguments of an api.* call, the shape daemon.call
// takes: a key is the parameter's name without its leading p (pSearch →
// "search"); a key that is absent leaves the parameter to its DEFAULT, a
// nil value is an explicit NULL. A json.RawMessage is a JSON value (for a
// json/jsonb parameter), never a string holding JSON.
type Args map[string]any

// Typed pins the SQL type of a value where the parameter's name alone does
// not choose the overload (api.set_session_area(uuid | text)). It is a hint
// for the direct call only: daemon.call takes the signature its list
// registers, and the value travels as the value.
type Typed struct {
	V    any
	Type string
}

// MarshalJSON is the value, as daemon.call receives it.
func (t Typed) MarshalJSON() ([]byte, error) { return json.Marshal(t.V) }

// JSON is a JSON argument: nil when empty (an explicit NULL), else the
// value as json.RawMessage — never []byte, which would travel as base64.
func JSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

// JSON is the argument object daemon.call takes.
func (a Args) JSON() ([]byte, error) {
	if a == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(a))
}

var callIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// sqlTypes are the types a Typed value may be pinned to: the cast is text of
// SQL, so it is chosen from a list, never built.
var sqlTypes = map[string]bool{
	"uuid": true, "text": true, "integer": true, "bigint": true, "smallint": true, "numeric": true,
	"boolean": true, "date": true, "timestamp": true, "timestamptz": true, "json": true, "jsonb": true,
}

// callSQL builds the direct call: every key a named argument, in key order
// so that one call is one prepared statement; the function is of schema api
// ("api.list_user" and "list_user" name the same one) and nothing else.
func callSQL(fn string, args Args) (string, []any, error) {
	name := strings.TrimPrefix(fn, "api.")
	if !callIdent.MatchString(name) {
		return "", nil, fmt.Errorf("pgtx: %q is not a function of schema api", fn)
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		if !callIdent.MatchString(k) {
			return "", nil, fmt.Errorf("pgtx: %s: %q is not a parameter name", fn, k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	vals := make([]any, len(keys))
	for i, k := range keys {
		v := args[k]
		if _, bare := v.([]byte); bare {
			// daemon.call would get base64 in a JSON string: JSON goes as json.RawMessage (pgtx.JSON)
			return "", nil, fmt.Errorf("pgtx: %s: %q is []byte — pass JSON as json.RawMessage", fn, k)
		}
		cast := ""
		if t, ok := v.(Typed); ok {
			if !sqlTypes[t.Type] {
				return "", nil, fmt.Errorf("pgtx: %s: %q is not a type name", fn, t.Type)
			}
			v, cast = t.V, "::"+t.Type
		}
		parts[i] = fmt.Sprintf("p%s => $%d%s", k, i+1, cast)
		vals[i] = v
	}
	return "SELECT to_json(t) FROM api." + name + "(" + strings.Join(parts, ", ") + ") t", vals, nil
}

// Call runs one function of schema api inside the request's transaction —
// through daemon.call on the daemon road, as a direct named call before it —
// and returns its rows as JSON: a row of a SETOF composite is an object, a
// scalar is its value, void is one row whose value means nothing; no rows is
// an empty, non-nil slice. This is the only way the platform's
// packages reach the database — under the daemon role there is no text of
// SQL to send over schema api, only the call.
func Call(ctx context.Context, tx pgx.Tx, fn string, args Args) ([]json.RawMessage, error) {
	if onDaemonRoad(ctx, tx) {
		return callDaemon(ctx, tx, fn, args)
	}
	sql, vals, err := callSQL(fn, args)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{} // no rows is an empty array on the wire, never null
	for rows.Next() {
		var row []byte
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ErrNoRow is CallRow's answer when the function returned no row.
var ErrNoRow = errors.New("pgtx: no row")

// CallRow is Call for a function of one row: its first row, or ErrNoRow.
func CallRow(ctx context.Context, tx pgx.Tx, fn string, args Args) (json.RawMessage, error) {
	rows, err := Call(ctx, tx, fn, args)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoRow
	}
	return rows[0], nil
}

// CallScalar decodes the one value of a scalar function into v; a NULL
// leaves v untouched.
func CallScalar(ctx context.Context, tx pgx.Tx, fn string, args Args, v any) error {
	row, err := CallRow(ctx, tx, fn, args)
	if err != nil || row == nil {
		return err
	}
	return json.Unmarshal(row, v)
}
