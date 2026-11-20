package error

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Row is a row of the view api.error_catalog: one code of the error
// catalogue, its texts in the session's locale.
type Row struct {
	ID          string  `json:"id" format:"uuid" doc:"Identifier of the catalogue entry."`
	Code        string  `json:"code" doc:"The code, ERR-GGG-CCC."`
	HTTPCode    int32   `json:"http_code" doc:"HTTP status of the code."`
	Severity    string  `json:"severity" doc:"Severity, one character."`
	Category    string  `json:"category" doc:"Category of the error."`
	Message     *string `json:"message" doc:"Message in the session's locale, or null where the code has no text in it."`
	Description *string `json:"description" doc:"Description in the session's locale, or null."`
	Resolution  *string `json:"resolution" doc:"What to do about it, in the session's locale, or null."`
	CreatedAt   string  `json:"created_at" format:"date-time" doc:"Time the code was added to the catalogue."`
}

// Input is the body of POST /errors.
type Input struct {
	Code        *string `json:"code" required:"create" doc:"The code, ERR-GGG-CCC: three digits for the HTTP group, three for the number."`
	HTTPCode    *int    `json:"http_code" required:"create" doc:"HTTP status of the code."`
	Severity    *string `json:"severity" doc:"Severity, one character; E when absent."`
	Category    *string `json:"category" doc:"Category of the error; validation when absent."`
	Message     *string `json:"message" doc:"Message, written for the session's locale."`
	Description *string `json:"description" doc:"Description, written with the message."`
	Resolution  *string `json:"resolution" doc:"What to do about it, written with the message."`
}

// PatchInput is the body of PATCH /errors/{id}: the keys of Input, none required.
type PatchInput struct {
	Code        *string `json:"code" doc:"The code, ERR-GGG-CCC."`
	HTTPCode    *int    `json:"http_code" doc:"HTTP status of the code."`
	Severity    *string `json:"severity" doc:"Severity, one character."`
	Category    *string `json:"category" doc:"Category of the error."`
	Message     *string `json:"message" doc:"Message for the session's locale; the message, the description and the resolution are written only when it is given."`
	Description *string `json:"description" doc:"Description, written with the message."`
	Resolution  *string `json:"resolution" doc:"What to do about it, written with the message."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	p := errors.Prefix
	row := openapi.Schema{Name: "ErrorCatalog", Value: Row{}, Mode: openapi.Row,
		Description: "A row of api.error_catalog: a code of the error catalogue and its texts in the session's locale. Every property is present, a property that has no value is null. In a list answered with `fields`, only the named properties are present."}
	in := openapi.Schema{Name: "ErrorCatalogInput", Value: Input{}, Mode: openapi.Create,
		Description: "Body of POST " + p + ". An absent key and an explicit null both mean \"not set\", except for a required key: it must be present and not empty (400 otherwise); a key the schema does not name is refused with 400."}
	patch := openapi.Schema{Name: "ErrorCatalogPatch", Value: PatchInput{}, Mode: openapi.Create,
		Description: "Body of PATCH " + p + "/{id}. An absent key and an explicit null both mean \"do not change\"; a key the schema does not name is refused with 400."}
	etag := openapi.Header{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}
	id := openapi.PathID("id", "Identifier of the catalogue entry.")
	admin := " Reading is open to any session; writing is the administrators' alone."
	codeBad := "code is not of the form ERR-GGG-CCC"
	return []openapi.Doc{
		{Method: "GET", Path: p, ID: "errors_list", Tag: "errors",
			Summary:     "List the codes of the error catalogue",
			Description: "The codes of the catalogue with their texts in the session's locale, filtered, sorted and paged (api.list_error and api.count_error). total counts the rows of the same selection before paging." + admin,
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(row)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		{Method: "GET", Path: p + "/by-code/{code}", ID: "errors_by_code", Tag: "errors",
			Summary:     "Get a catalogue entry by its code",
			Description: "One entry by the value of its code, api.get_error_by_code. The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{openapi.PathText("code", "The code, ERR-GGG-CCC; anything else is refused with 400."), {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK:       []openapi.Response{{Status: "200", Description: "The entry.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The code is not of the form ERR-GGG-CCC.",
			NotFound: "The catalogue has no such code.",
			Extra:    []openapi.Response{{Status: "304", Description: "The entry has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}}}},
		{Method: "GET", Path: p + "/{id}", ID: "errors_get", Tag: "errors",
			Summary:     "Get a catalogue entry",
			Description: "One entry by its identifier, api.get_error. The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{id, {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK:       []openapi.Response{{Status: "200", Description: "The entry.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The id is not a UUID.",
			NotFound: "There is no such entry.",
			Extra:    []openapi.Response{{Status: "304", Description: "The entry has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}}}},
		{Method: "POST", Path: p, ID: "errors_create", Tag: "errors",
			Summary:     "Add a code to the catalogue",
			Description: "api.set_error with no id: the code is added, with its texts when a message is given. The catalogue has no delete." + admin + " One request is one transaction; the answer is the new entry.",
			Params: []openapi.Param{{Name: "Idempotency-Key", In: "header", Schema: openapi.String,
				Description: "Optional. The same key with the same body replays the stored answer (marked Idempotency-Replayed: true) for 24 hours; the same key with another body is 409. A key is scoped to the session and the path; a 5xx answer is not stored. The store is in the memory of one service process: a replay is not found after a restart, nor on another instance."}},
			Body: &openapi.Body{Required: true, Schema: in},
			OK: []openapi.Response{{Status: "201", Description: "Created.", Schema: row, Headers: []openapi.Header{
				{Name: "Location", Description: "Path of the new entry.", Schema: openapi.String}, etag}}},
			Bad:   "No body, an unknown key, a value of the wrong type, " + codeBad + ", a missing code or http_code, or a value the database refuses.",
			Extra: []openapi.Response{{Status: "409", Problem: true, Description: "Idempotency-Key reused with a different body, or the database reports a conflict (the code already exists)."}}},
		{Method: "PATCH", Path: p + "/{id}", ID: "errors_update", Tag: "errors",
			Summary:     "Change a catalogue entry",
			Description: "api.set_error with the id: the given keys change. If-Match is required: the ETag of the revision the client read." + admin,
			Params: []openapi.Param{id, {Name: "If-Match", In: "header", Required: true, Schema: openapi.String,
				Description: "The ETag of the row as read; * matches any current row. Without it the answer is 428 and the body is not read."}},
			Body:     &openapi.Body{Required: true, Schema: patch},
			OK:       []openapi.Response{{Status: "200", Description: "The entry after the change.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The id is not a UUID, an unknown key, a value of the wrong type, a code that is not of the form ERR-GGG-CCC or is empty, or a value the database refuses.",
			NotFound: "There is no such entry.",
			Extra: []openapi.Response{
				{Status: "412", Problem: true, Description: "The row changed since it was read: If-Match is no longer its ETag."},
				{Status: "428", Problem: true, Description: "No If-Match header."}}},
	}
}
