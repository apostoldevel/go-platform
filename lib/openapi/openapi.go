// Package openapi builds the OpenAPI 3.1 description of /api/v2 from the
// source text of the modules: the routes a module declares, the Go types of
// its request bodies and rows (their json and doc tags), the answers its
// handlers give. Nothing here reads a database or a SQL file — the document
// is a function of the Go code, so that `go test` can say whether it is still
// true (Gaps, the freshness test of the project) and a release can regenerate
// it with one command.
//
// A module that wants to be described implements Documented. A module that
// does not is simply absent from the document — the build's manifest decides
// what is mounted, the module decides whether it is described, and a test of
// the project lists the described ones so that the list only grows on
// purpose.
//
// Schemas come from struct tags:
//
//	json:"name"            the property; "-" skips; embedded structs are flattened
//	doc:"text"             description of the property
//	format:"uuid"          overrides the format of the Go type
//	enum:"a,b"             the allowed values (strings)
//	required:"create"      required in the create schema (Create), where it is
//	                       also neither null nor empty; the update schema
//	                       (Patch) requires none and at least one key
//
// A pointer is nullable ("type": ["string", "null"]). In a Row every property
// without omitempty is required — a row has all its columns.
package openapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/apostoldevel/go-platform/lib/problem"
)

// Documented is a platform.Module that describes its routes. The list is the
// whole of what the module mounts: Gaps compares it with the mux.
type Documented interface {
	Operations() []Operation
}

// Info is the document's header.
type Info struct {
	Title       string
	Version     string
	Description string
	// Extensions are x-… members of info, in the document as given.
	Extensions map[string]any
}

// Operation is one route: "METHOD /path/{param}" and what is known of it.
type Operation struct {
	Method      string // GET, POST, PATCH, DELETE …
	Path        string // "/api/v2/currencies/{id}"
	ID          string // operationId, unique in the document
	Summary     string
	Description string
	Tag         string
	Params      []Param
	Body        *Body
	Responses   []Response
}

// Pattern is the operation as http.ServeMux spells it.
func (o Operation) Pattern() string { return o.Method + " " + o.Path }

// Param is a path, query or header parameter.
type Param struct {
	Name        string
	In          string // path | query | header
	Description string
	Required    bool
	Schema      any // a schema node (see Build)
	// Deep marks a query parameter that carries its keys in brackets
	// (filter[field][op]=…): style deepObject, as far as OpenAPI can say it.
	Deep bool
}

// Body is a request body.
type Body struct {
	Description string
	Required    bool
	Schema      any // a schema node
}

// Header is a response header.
type Header struct {
	Name        string
	Description string
	Schema      any
}

// Response is one answer. Status is "200", "4XX" or "default".
type Response struct {
	Status      string
	Description string
	Schema      any // JSON body; nil for none
	Problem     bool
	Headers     []Header
}

// Mode picks the variant of a struct's schema.
type Mode int

const (
	Row    Mode = iota // every property without omitempty is required
	Create             // required are the properties tagged required:"create"
	Patch              // none required, at least one property
)

// Schema names a struct type as a component of the document: a value of it
// in a schema node becomes a $ref, the component is built once.
type Schema struct {
	Name        string
	Value       any // a struct, or a pointer to one
	Mode        Mode
	Description string
}

// Build assembles the document from the operations. A schema node in an
// Operation is a Schema (→ $ref), a map[string]any or []any holding more
// nodes, or any other value taken as it is (a ready JSON-Schema fragment).
// The document is deterministic: paths, methods and components are ordered,
// nothing depends on time or on the map order of the caller.
func Build(info Info, ops []Operation) (map[string]any, error) {
	b := &builder{schemas: map[string]any{}, source: map[string]Schema{}}
	problemSchema := Schema{Name: "Problem", Value: problem.Problem{}, Mode: Row,
		Description: "application/problem+json (RFC 9457). Catalogue errors carry the ERR-GGG-CCC code in `type` and `code`; the HTTP status is GGG when that is a valid status, else 400."}
	b.schema(problemSchema)

	paths := map[string]any{}
	seen := map[string]string{}
	for _, o := range ops {
		if o.Method == "" || !strings.HasPrefix(o.Path, "/") || o.ID == "" {
			return nil, fmt.Errorf("openapi: operation %q needs method, path and id", o.Pattern())
		}
		if prev, dup := seen[o.ID]; dup {
			return nil, fmt.Errorf("openapi: operationId %q is both %s and %s", o.ID, prev, o.Pattern())
		}
		seen[o.ID] = o.Pattern()
		item, _ := paths[o.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[o.Path] = item
		}
		method := strings.ToLower(o.Method)
		if _, dup := item[method]; dup {
			return nil, fmt.Errorf("openapi: %s is described twice", o.Pattern())
		}
		op, err := b.operation(o)
		if err != nil {
			return nil, fmt.Errorf("openapi: %s: %w", o.Pattern(), err)
		}
		item[method] = op
	}

	infoMap := map[string]any{"title": info.Title, "version": info.Version}
	if info.Description != "" {
		infoMap["description"] = info.Description
	}
	for k, v := range info.Extensions {
		infoMap[k] = v
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    infoMap,
		"paths":   paths,
		"components": map[string]any{
			"schemas": b.schemas,
			"securitySchemes": map[string]any{"bearerAuth": map[string]any{
				"type": "http", "scheme": "bearer", "bearerFormat": "JWT",
				"description": "The access token of the OAuth 2.0 provider, verified before any route.",
			}},
		},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
	}, nil
}

// Marshal writes the document as indented JSON with a trailing newline —
// OpenAPI allows JSON, and the keys come out sorted.
func Marshal(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Gaps compares what the module mounted on mux with what it described.
// mounted lists a documented pattern the mux does not serve; undocumented
// lists a pattern the mux serves on a documented path that no operation
// describes (every method is probed on every documented path, so an extra
// DELETE or PUT is seen). What it cannot see is a whole new path no
// operation names — a mux cannot be enumerated — and what the project's test
// of the described modules adds is the explicit list of them.
func Gaps(mux *http.ServeMux, ops []Operation) (missing, undocumented []string) {
	want := map[string]bool{}
	paths := map[string]bool{}
	for _, o := range ops {
		want[o.Pattern()] = true
		paths[o.Path] = true
	}
	probe := func(method, path string) string {
		r, err := http.NewRequest(method, "http://x"+concrete(path), nil)
		if err != nil {
			return ""
		}
		_, pat := mux.Handler(r)
		return pat
	}
	for _, o := range ops {
		if probe(o.Method, o.Path) != o.Pattern() {
			missing = append(missing, o.Pattern())
		}
	}
	got := map[string]bool{}
	for p := range paths {
		for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
			if pat := probe(m, p); pat != "" && !want[pat] && !got[pat] {
				got[pat] = true
				undocumented = append(undocumented, pat)
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(undocumented)
	return missing, undocumented
}

// concrete turns a path template into a path the mux can route: every
// {name} becomes a segment, {name...} a few.
func concrete(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '{' {
			out.WriteByte(path[i])
			i++
			continue
		}
		j := strings.IndexByte(path[i:], '}')
		if j < 0 {
			out.WriteString(path[i:])
			break
		}
		out.WriteString("v")
		i += j + 1
	}
	return out.String()
}

// ── the builder ────────────────────────────────────────────────────────

type builder struct {
	schemas map[string]any
	source  map[string]Schema // by name, to refuse one name for two different schemas
}

func (b *builder) operation(o Operation) (map[string]any, error) {
	out := map[string]any{"operationId": o.ID}
	if o.Summary != "" {
		out["summary"] = o.Summary
	}
	if o.Description != "" {
		out["description"] = o.Description
	}
	if o.Tag != "" {
		out["tags"] = []any{o.Tag}
	}
	declared := map[string]bool{}
	var params []any
	for _, p := range o.Params {
		if p.In != "path" && p.In != "query" && p.In != "header" {
			return nil, fmt.Errorf("parameter %q: in %q", p.Name, p.In)
		}
		pm := map[string]any{"name": p.Name, "in": p.In, "schema": b.node(p.Schema)}
		if p.Description != "" {
			pm["description"] = p.Description
		}
		if p.Required || p.In == "path" {
			pm["required"] = true
		}
		if p.Deep {
			pm["style"], pm["explode"] = "deepObject", true
		}
		if p.In == "path" {
			declared[p.Name] = true
		}
		params = append(params, pm)
	}
	for _, name := range pathParams(o.Path) {
		if !declared[name] {
			return nil, fmt.Errorf("path parameter {%s} is not declared", name)
		}
		delete(declared, name)
	}
	for name := range declared {
		return nil, fmt.Errorf("path parameter %q is declared and not in the path", name)
	}
	if params != nil {
		out["parameters"] = params
	}
	if o.Body != nil {
		rb := map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": b.node(o.Body.Schema)}}}
		if o.Body.Description != "" {
			rb["description"] = o.Body.Description
		}
		if o.Body.Required {
			rb["required"] = true
		}
		out["requestBody"] = rb
	}
	if len(o.Responses) == 0 {
		return nil, fmt.Errorf("no responses")
	}
	responses := map[string]any{}
	for _, r := range o.Responses {
		if _, dup := responses[r.Status]; dup {
			return nil, fmt.Errorf("response %s twice", r.Status)
		}
		rm := map[string]any{"description": r.Description}
		switch {
		case r.Problem:
			rm["content"] = map[string]any{"application/problem+json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Problem"}}}
		case r.Schema != nil:
			rm["content"] = map[string]any{"application/json": map[string]any{"schema": b.node(r.Schema)}}
		}
		if len(r.Headers) > 0 {
			hs := map[string]any{}
			for _, h := range r.Headers {
				hm := map[string]any{"schema": b.node(h.Schema)}
				if h.Description != "" {
					hm["description"] = h.Description
				}
				hs[h.Name] = hm
			}
			rm["headers"] = hs
		}
		responses[r.Status] = rm
	}
	out["responses"] = responses
	return out, nil
}

func pathParams(path string) []string {
	var out []string
	for rest := path; ; {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return out
		}
		out = append(out, strings.TrimSuffix(rest[i+1:i+j], "..."))
		rest = rest[i+j+1:]
	}
}

// node resolves a schema node: Schema → $ref, containers recursively.
func (b *builder) node(n any) any {
	switch v := n.(type) {
	case Schema:
		b.schema(v)
		return map[string]any{"$ref": "#/components/schemas/" + v.Name}
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = b.node(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = b.node(e)
		}
		return out
	default:
		return n
	}
}

func (b *builder) schema(s Schema) {
	if prev, ok := b.source[s.Name]; ok {
		if prev.Mode != s.Mode || reflect.TypeOf(prev.Value) != reflect.TypeOf(s.Value) {
			panic(fmt.Sprintf("openapi: schema name %q is given to two different schemas", s.Name))
		}
		return
	}
	b.source[s.Name] = s
	t := reflect.TypeOf(s.Value)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("openapi: schema %q is %s, not a struct", s.Name, t))
	}
	out := structSchema(t, s.Mode)
	if s.Description != "" {
		out["description"] = s.Description
	}
	b.schemas[s.Name] = out
}

var timeType = reflect.TypeFor[time.Time]()

func structSchema(t reflect.Type, mode Mode) map[string]any {
	props := map[string]any{}
	required := []any{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
				continue
			}
			if !f.IsExported() {
				continue
			}
			key, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if key == "-" || key == "" {
				continue
			}
			s := typeSchema(f.Type)
			if d := f.Tag.Get("doc"); d != "" {
				s["description"] = d
			}
			if fm := f.Tag.Get("format"); fm != "" {
				s["format"] = fm
			}
			if e := f.Tag.Get("enum"); e != "" {
				var vals []any
				for _, v := range strings.Split(e, ",") {
					vals = append(vals, v)
				}
				s["enum"] = vals
			}
			props[key] = s
			switch mode {
			case Row:
				if !strings.Contains(opts, "omitempty") {
					required = append(required, key)
				}
			case Create:
				if f.Tag.Get("required") == "create" {
					required = append(required, key)
					// rest.Required refuses a null and an empty string alike.
					if ts, ok := s["type"].([]any); ok && len(ts) == 2 {
						s["type"] = ts[0]
					}
					if s["type"] == "string" {
						s["minLength"] = 1
					}
				}
			}
		}
	}
	walk(t)
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	switch mode {
	case Create, Patch:
		out["additionalProperties"] = false // a body key the struct does not know is refused with 400
	}
	if mode == Patch {
		out["minProperties"] = 1
	}
	return out
}

func typeSchema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return nullable(typeSchema(t.Elem()))
	}
	switch {
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case t == reflect.TypeFor[json.RawMessage]():
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16:
		return map[string]any{"type": "integer", "format": "int32"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "format": "int64"}
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": typeSchema(t.Elem())}
	case reflect.Struct:
		return structSchema(t, Row)
	}
	panic(fmt.Sprintf("openapi: no schema for %s", t))
}

// nullable adds "null" to the type of a schema ({} stays as it is: it
// already allows null).
func nullable(s map[string]any) map[string]any {
	if typ, ok := s["type"].(string); ok {
		s["type"] = []any{typ, "null"}
	}
	return s
}
