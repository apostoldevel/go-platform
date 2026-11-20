package openapi

import (
	"github.com/apostoldevel/go-platform/lib/query"
)

// Doc is the description of one route a module mounts beside the standard
// routes of an entity form, in the words the document uses. Operation fills what
// is the same for every route (the three answers of the gateway road, and the 400
// and the 404 where the route can give them), so a module states only what is
// its own. A module returns its Docs next to the form's from Operations; a test of
// the project holds them to the mux.
type Doc struct {
	Method      string
	Path        string
	ID          string // operationId: <noun>_<verb>, unique in the document
	Tag         string // the group: the path segment after /api/v2/
	Summary     string
	Description string
	Params      []Param
	Body        *Body
	// OK is the success answer: Status "200" unless it says otherwise.
	OK []Response
	// Bad says what a 400 of this route is; empty when it cannot give one.
	Bad string
	// NotFound says what a 404 of this route is; empty when it cannot give one.
	NotFound string
	// Extra are the other answers of the route (409, 412, 413 …).
	Extra []Response
}

// Operation is the description in the form Build takes.
func (d Doc) Operation() Operation {
	rs := append([]Response(nil), d.OK...)
	if d.Bad != "" {
		rs = append(rs, Response{Status: "400", Problem: true, Description: d.Bad})
	}
	if d.NotFound != "" {
		rs = append(rs, Response{Status: "404", Problem: true, Description: d.NotFound})
	}
	rs = append(rs, d.Extra...)
	rs = append(rs,
		Response{Status: "401", Problem: true, Description: "No bearer token, a token that does not verify or has expired, or a session the database does not know. Carries WWW-Authenticate."},
		Response{Status: "403", Problem: true, Description: "The database refused: the route is not open to this session, or a right is missing (ERR-403-…)."},
		Response{Status: "default", Problem: true, Description: "Any other refusal of the error catalogue: the status is the GGG of its ERR-GGG-CCC code (400 where that is not a status). A failure of the service itself is 500 with type urn:apostol:error:internal."})
	return Operation{
		Method: d.Method, Path: d.Path, ID: d.ID, Tag: d.Tag,
		Summary: d.Summary, Description: d.Description,
		Params: d.Params, Body: d.Body, Responses: rs,
	}
}

// Docs turns descriptions into operations.
func Docs(ds ...Doc) []Operation {
	out := make([]Operation, len(ds))
	for i, d := range ds {
		out[i] = d.Operation()
	}
	return out
}

// Schema nodes the answers are made of.

// ObjectOf is the schema of one row of a struct (every property present, a
// property without a value null).
func ObjectOf(name string, row any, doc string) Schema {
	return Schema{Name: name, Value: row, Mode: Row, Description: doc}
}

// Page is the answer of a list: the rows of one page and what the page is.
func Page(item any) map[string]any {
	return map[string]any{
		"type": "object", "required": []any{"items", "total", "limit", "offset"},
		"properties": map[string]any{
			"items":  map[string]any{"type": "array", "items": item},
			"total":  map[string]any{"type": "integer", "format": "int64", "description": "Number of rows of the whole selection."},
			"limit":  map[string]any{"type": "integer", "description": "Page size applied."},
			"offset": map[string]any{"type": "integer", "description": "Rows skipped."},
		}}
}

// PageMore is the answer of a list that has no count function: the rows of one
// page, the limit and offset applied, and has_more — whether another page
// follows. There is no total.
func PageMore(item any) map[string]any {
	return map[string]any{
		"type": "object", "required": []any{"items", "limit", "offset", "has_more"},
		"properties": map[string]any{
			"items":    map[string]any{"type": "array", "items": item},
			"limit":    map[string]any{"type": "integer", "description": "Page size applied."},
			"offset":   map[string]any{"type": "integer", "description": "Rows skipped."},
			"has_more": map[string]any{"type": "boolean", "description": "True when another page follows."},
		}}
}

// Array is a plain array of items (an answer that is not paged).
func Array(item any) map[string]any { return map[string]any{"type": "array", "items": item} }

// Obj is an object of the given properties, all required; props are
// name → schema node, in no particular order.
func Obj(doc string, props map[string]any) map[string]any {
	req := make([]any, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	sortAny(req)
	return map[string]any{"type": "object", "description": doc, "required": req, "properties": props}
}

// OK200 is the 200 answer with a schema.
func OK200(desc string, schema any) []Response {
	return []Response{{Status: "200", Description: desc, Schema: schema}}
}

// Scalars of the nodes.
var (
	String   = map[string]any{"type": "string"}
	UUIDNode = map[string]any{"type": "string", "format": "uuid"}
	Integer  = map[string]any{"type": "integer"}
	Number   = map[string]any{"type": "number"}
	Boolean  = map[string]any{"type": "boolean"}
	Time     = map[string]any{"type": "string", "format": "date-time"}
	Any      = map[string]any{}
)

// Nullable makes a scalar node nullable.
func Nullable(typ string, format string) map[string]any {
	n := map[string]any{"type": []any{typ, "null"}}
	if format != "" {
		n["format"] = format
	}
	return n
}

// Parameters.

// PathID is a path wildcard that is a UUID.
func PathID(name, desc string) Param {
	return Param{Name: name, In: "path", Required: true, Schema: UUIDNode, Description: desc + " A UUID; anything else is refused with 400 before the database."}
}

// PathInt is a path wildcard that is an integer.
func PathInt(name, desc string) Param {
	return Param{Name: name, In: "path", Required: true, Schema: Integer, Description: desc + " An integer; anything else is refused with 400 before the database."}
}

// PathText is a path wildcard that is a text.
func PathText(name, desc string) Param {
	return Param{Name: name, In: "path", Required: true, Schema: String, Description: desc}
}

// Q is a query parameter of the given schema node.
func Q(name string, schema any, required bool, desc string) Param {
	return Param{Name: name, In: "query", Required: required, Schema: schema, Description: desc}
}

// QID is a UUID query parameter.
func QID(name string, required bool, desc string) Param {
	return Q(name, UUIDNode, required, desc)
}

// ListParams are the parameters of a list built by ListSpec: filter, sort,
// fields, page[limit], page[offset] and, with search, q.
func ListParams(search ...string) []Param {
	ps := []Param{
		{Name: "filter", In: "query", Deep: true, Schema: map[string]any{"type": "object", "additionalProperties": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}}},
			Description: "Conditions on the row's properties: filter[<property>]=<value>, or filter[<property>][<operator>]=<value> with the operator one of eq, ne, lt, lte, gt, gte, like, ilike, in (comma-separated values), null. Conditions are joined with AND."},
		{Name: "sort", In: "query", Schema: String,
			Description: "Comma-separated properties; a leading - is descending: sort=-created,name."},
		{Name: "fields", In: "query", Schema: String,
			Description: "Comma-separated properties to return; the others are left out of each item."},
		{Name: "page[limit]", In: "query", Schema: map[string]any{"type": "integer", "minimum": 1, "maximum": query.MaxLimit, "default": query.DefaultLimit}, Description: "Page size."},
		{Name: "page[offset]", In: "query", Schema: map[string]any{"type": "integer", "minimum": 0, "default": 0}, Description: "Rows to skip."},
	}
	if len(search) > 0 {
		ps = append(ps, Param{Name: "q", In: "query", Schema: String,
			Description: "One term, matched without regard to case; given once."})
	}
	return ps
}

func sortAny(a []any) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].(string) < a[j-1].(string); j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
