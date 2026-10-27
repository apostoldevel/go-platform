package entity

import (
	"net/http"
	"strings"

	"github.com/apostoldevel/go-platform/lib/openapi"
	"github.com/apostoldevel/go-platform/lib/query"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// MethodRow is a row of api.get_object_methods (view api.method of the
// workflow module) as GET <xs>/{id}/methods answers it: a method of the
// object's class that is bound to its current state, with what the caller's
// rights say of it.
type MethodRow struct {
	ID         string  `json:"id" format:"uuid" doc:"Identifier of the method."`
	Parent     *string `json:"parent" format:"uuid" doc:"Parent method (a nested or sub-menu method), or null."`
	Entity     string  `json:"entity" format:"uuid" doc:"Entity of the class."`
	EntityCode string  `json:"entitycode" doc:"Code of the entity."`
	EntityName *string `json:"entityname" doc:"Name of the entity in the session's locale, or null where it has no text."`
	Class      string  `json:"class" format:"uuid" doc:"Class the method belongs to."`
	ClassCode  string  `json:"classcode" doc:"Code of the class."`
	ClassLabel *string `json:"classlabel" doc:"Label of the class in the session's locale, or null where it has no text."`
	State      *string `json:"state" format:"uuid" doc:"State in which the method is available; null means any state."`
	StateCode  *string `json:"statecode" doc:"Code of that state, or null."`
	StateLabel *string `json:"statelabel" doc:"Label of that state in the session's locale, or null."`
	Action     string  `json:"action" format:"uuid" doc:"Action the method performs."`
	ActionCode string  `json:"actioncode" doc:"Code of the action: the {action} of POST <xs>/{id}/actions/{action}."`
	ActionName *string `json:"actionname" doc:"Name of the action in the session's locale, or null."`
	Code       string  `json:"code" doc:"Code of the method, state_code:action_code."`
	Label      *string `json:"label" doc:"Label of the method in the session's locale, or null."`
	Sequence   int32   `json:"sequence" doc:"Display and processing order; the answer is in this order."`
	Execute    bool    `json:"execute" doc:"The caller may execute the method on this object."`
	Visible    bool    `json:"visible" doc:"The method is shown to the caller."`
	Enable     bool    `json:"enable" doc:"The method is enabled for the caller."`
}

// formRoute is one of the six routes of the form: what the mux gets and what
// the description says of it, kept in one place so the two cannot drift.
type formRoute struct {
	op openapi.Operation
	h  http.HandlerFunc
}

// Operations is the module's description of its routes: the six of the
// form, for an entity that declares its Row; nil otherwise — an entity
// without a Row is not described yet, and says so by being absent from the
// document. A package that adds routes of its own to the six must not
// declare a Row until it describes them too (openapi.Gaps tests it).
func (m *Module) Operations() []openapi.Operation {
	if m.Row == nil {
		return nil
	}
	routes := m.form()
	ops := make([]openapi.Operation, len(routes))
	for i, r := range routes {
		ops[i] = r.op
	}
	return ops
}

var _ openapi.Documented = (*Module)(nil)

// form is the six routes. Routes registers them; Operations describes them.
func (m *Module) form() []formRoute {
	p := m.Prefix
	plural := strings.TrimPrefix(p, "/api/v2/")
	words := strings.NewReplacer("-", " ", "_", " ")
	noun, nouns := words.Replace(m.Name()), words.Replace(plural)
	id := strings.NewReplacer("-", "_").Replace(plural)
	pascal := pascalOf(m.Name())

	var row, input, patch any = map[string]any{"type": "object", "additionalProperties": true,
		"description": "A row of api." + m.Name() + "; its fields are not described yet."}, nil, nil
	if m.Row != nil {
		row = openapi.Schema{Name: pascal, Value: m.Row, Mode: openapi.Row,
			Description: "A row of api." + m.Name() + " as the database gives it: every property is present, a property that has no value is null. In a list answered with `fields`, only the named properties are present."}
	}
	if b := m.NewBody(); b != nil {
		input = openapi.Schema{Name: pascal + "Input", Value: b, Mode: openapi.Create,
			Description: "Body of POST " + p + ". An absent key and an explicit null both mean \"not set\", except for a required key: it must be present and not empty (400 otherwise); a key the schema does not name is refused with 400."}
		patch = openapi.Schema{Name: pascal + "Patch", Value: b, Mode: openapi.Patch,
			Description: "Body of PATCH " + p + "/{id}. An absent key and an explicit null both mean \"do not change\"; at least one key with a value (not null) is required; a key the schema does not name is refused with 400."}
	}

	uuid := map[string]any{"type": "string", "format": "uuid"}
	idParam := openapi.Param{Name: "id", In: "path", Description: "Identifier of the " + noun + ": a UUID. Anything else is refused with 400 before the database.", Schema: uuid}
	etag := openapi.Header{Name: "ETag", Description: "Weak ETag of the row: its last update, or a digest of the row.", Schema: map[string]any{"type": "string"}}

	errs := func(rs ...openapi.Response) []openapi.Response {
		return append(rs,
			openapi.Response{Status: "401", Problem: true, Description: "No bearer token, a token that does not verify or has expired, or a session the database does not know. Carries WWW-Authenticate."},
			openapi.Response{Status: "403", Problem: true, Description: "The database refused: the route is not open to this session, or a right is missing (ERR-403-…)."},
			openapi.Response{Status: "default", Problem: true, Description: "Any other refusal of the error catalogue: the status is the GGG of its ERR-GGG-CCC code (400 where that is not a status). A failure of the service itself is 500 with type urn:apostol:error:internal."})
	}
	bad := func(what string) openapi.Response {
		return openapi.Response{Status: "400", Problem: true, Description: what}
	}
	notFound := openapi.Response{Status: "404", Problem: true, Description: "There is no such " + noun + ", or the caller may not see it — the two are not told apart."}
	ok := func(desc string, h ...openapi.Header) openapi.Response {
		return openapi.Response{Status: "200", Description: desc, Schema: row, Headers: h}
	}

	listParams := []openapi.Param{
		{Name: "filter", In: "query", Deep: true, Schema: map[string]any{"type": "object", "additionalProperties": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}}},
			Description: "Conditions on the row's properties: filter[<property>]=<value>, or filter[<property>][<operator>]=<value> with the operator one of eq, ne, lt, lte, gt, gte, like, ilike, in (comma-separated values), null. Conditions are joined with AND."},
		{Name: "sort", In: "query", Schema: map[string]any{"type": "string"},
			Description: "Comma-separated properties; a leading - is descending: sort=-created,name."},
		{Name: "fields", In: "query", Schema: map[string]any{"type": "string"},
			Description: "Comma-separated properties to return; the others are left out of each item."},
		{Name: "page[limit]", In: "query", Schema: map[string]any{"type": "integer", "minimum": 1, "maximum": query.MaxLimit, "default": query.DefaultLimit},
			Description: "Page size."},
		{Name: "page[offset]", In: "query", Schema: map[string]any{"type": "integer", "minimum": 0, "default": 0},
			Description: "Rows to skip."},
	}
	if len(m.SearchFields) > 0 {
		listParams = append(listParams, openapi.Param{Name: "q", In: "query", Schema: map[string]any{"type": "string"},
			Description: "One term, matched without regard to case over " + strings.Join(m.SearchFields, ", ") + "; given once."})
	}

	return []formRoute{
		{openapi.Operation{
			Method: "GET", Path: p, ID: id + "_list", Tag: plural,
			Summary:     "List " + nouns,
			Description: "The " + nouns + " the caller may see, filtered, sorted and paged. total counts the rows of the same selection before paging.",
			Params:      listParams,
			Responses: errs(openapi.Response{Status: "200", Description: "The page.", Schema: map[string]any{
				"type": "object", "required": []any{"items", "total", "limit", "offset"},
				"properties": map[string]any{
					"items":  map[string]any{"type": "array", "items": row},
					"total":  map[string]any{"type": "integer", "format": "int64", "description": "Number of rows of the whole selection."},
					"limit":  map[string]any{"type": "integer", "description": "Page size applied."},
					"offset": map[string]any{"type": "integer", "description": "Rows skipped."},
				}}},
				bad("An unknown query parameter, or a bad filter, sort, fields or page value; q given more than once.")),
		}, m.List},
		{openapi.Operation{
			Method: "GET", Path: p + "/{id}", ID: id + "_get", Tag: plural,
			Summary: "Get a " + noun,
			Params: []openapi.Param{idParam, {Name: "If-None-Match", In: "header", Schema: map[string]any{"type": "string"},
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			Responses: errs(ok("The row.", etag),
				openapi.Response{Status: "304", Description: "The row has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}},
				bad("The id is not a UUID."), notFound),
		}, rest.RowFunc(m.Doer, m.Log, idArg, m.getRow)},
		{openapi.Operation{
			Method: "POST", Path: p, ID: id + "_create", Tag: plural,
			Summary:     "Create a " + noun,
			Description: "One request is one transaction. The answer is the new row.",
			Params: []openapi.Param{{Name: "Idempotency-Key", In: "header", Schema: map[string]any{"type": "string"},
				Description: "Optional. The same key with the same body replays the stored answer (marked Idempotency-Replayed: true) for 24 hours; the same key with another body is 409. A key is scoped to the session and the path; a 5xx answer is not stored. The store is in the memory of one service process: a replay is not found after a restart, nor on another instance."}},
			Body: &openapi.Body{Required: true, Schema: input},
			Responses: errs(openapi.Response{Status: "201", Description: "Created.", Schema: row, Headers: []openapi.Header{
				{Name: "Location", Description: "Path of the new row.", Schema: map[string]any{"type": "string"}}, etag}},
				bad("No body, an unknown key, a value of the wrong type, a required property missing, or a value the database refuses."),
				openapi.Response{Status: "409", Problem: true, Description: "Idempotency-Key reused with a different body, or the database reports a conflict."}),
		}, m.creator.Create(m.Doer, m.Idem, m.Log)},
		{openapi.Operation{
			Method: "PATCH", Path: p + "/{id}", ID: id + "_update", Tag: plural,
			Summary:     "Change a " + noun,
			Description: "Only the given keys change. If-Match is required: the ETag of the revision the client read.",
			Params: []openapi.Param{idParam, {Name: "If-Match", In: "header", Required: true, Schema: map[string]any{"type": "string"},
				Description: "The ETag of the row as read; * matches any current row. Without it the answer is 428 and the body is not read."}},
			Body: &openapi.Body{Required: true, Schema: patch},
			Responses: errs(ok("The row after the change.", etag),
				bad("The id is not a UUID, the body is empty or has no key, an unknown key, a value of the wrong type or one the database refuses."), notFound,
				openapi.Response{Status: "412", Problem: true, Description: "The row changed since it was read: If-Match is no longer its ETag."},
				openapi.Response{Status: "428", Problem: true, Description: "No If-Match header."}),
		}, m.Update},
		{openapi.Operation{
			Method: "POST", Path: p + "/{id}/actions/{action}", ID: id + "_action", Tag: plural,
			Summary:     "Execute an action of the " + noun,
			Description: "Runs a method of the object's workflow by the code of its action, on a row the caller may see. The methods available now are listed by GET …/methods. The answer is the row after the action; a row the action removed leaves none and the answer is 204.",
			Params: []openapi.Param{idParam, {Name: "action", In: "path", Schema: map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}$"},
				Description: "Code of the action (enable, disable, delete, drop, …)."}},
			Body: &openapi.Body{Description: "Parameters of the action. Optional; a JSON object, at most 1 MiB.",
				Schema: map[string]any{"type": "object", "additionalProperties": true}},
			Responses: errs(ok("The row after the action.", etag),
				openapi.Response{Status: "204", Description: "The action removed the row. No body."},
				bad("The id is not a UUID, the action name is malformed, or the body is not a JSON object."), notFound,
				openapi.Response{Status: "413", Problem: true, Description: "The body is over 1 MiB."}),
		}, m.Action},
		{openapi.Operation{
			Method: "GET", Path: p + "/{id}/methods", ID: id + "_methods", Tag: plural,
			Summary:     "Methods of the " + noun + " now",
			Description: "The methods of the object's class bound to its current state, with the caller's rights to each, in the workflow's order. Not paged.",
			Params: []openapi.Param{idParam, {Name: "fields", In: "query", Schema: map[string]any{"type": "string"},
				Description: "Comma-separated properties to return; the only query parameter taken."}},
			Responses: errs(openapi.Response{Status: "200", Description: "The methods.", Schema: map[string]any{
				"type": "array", "items": openapi.Schema{Name: "ObjectMethod", Value: MethodRow{}, Mode: openapi.Row,
					Description: "A method of an object's class, as GET <xs>/{id}/methods gives it."}}},
				bad("The id is not a UUID, or a query parameter other than fields."), notFound),
		}, m.Methods},
	}
}

// pascalOf turns an entity name (payment_method) into a component name (PaymentMethod).
func pascalOf(name string) string {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' }) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}
