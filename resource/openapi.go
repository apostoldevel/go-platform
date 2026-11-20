package resource

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Row is a row of the view api.resource: a node of the tree of resources with
// its content in the session's locale.
type Row struct {
	ID                string  `json:"id" format:"uuid" doc:"Identifier of the resource."`
	Root              string  `json:"root" format:"uuid" doc:"Root of the tree the resource belongs to."`
	Node              *string `json:"node" format:"uuid" doc:"The node above, or null."`
	Type              string  `json:"type" doc:"Media type of the content, such as text/plain."`
	Level             int32   `json:"level" doc:"Depth in the tree."`
	Sequence          int32   `json:"sequence" doc:"Order among the siblings."`
	Name              *string `json:"name" doc:"Name in the session's locale."`
	Description       *string `json:"description" doc:"Description in the session's locale."`
	Encoding          *string `json:"encoding" doc:"Encoding of the data."`
	Data              *string `json:"data" doc:"The content in the session's locale."`
	Updated           string  `json:"updated" format:"date-time" doc:"Time the content was last written."`
	Locale            string  `json:"locale" format:"uuid" doc:"Locale of the content."`
	LocaleCode        string  `json:"localecode" doc:"Code of the locale."`
	LocaleName        string  `json:"localename" doc:"Name of the locale."`
	LocaleDescription *string `json:"localedescription" doc:"Description of the locale."`
}

// Input is the body of POST /resources.
type Input struct {
	Root        *string `json:"root" format:"uuid" doc:"Root of the tree to put the resource in."`
	Node        *string `json:"node" format:"uuid" doc:"The node to put it under."`
	Type        *string `json:"type" doc:"Media type of the content."`
	Name        *string `json:"name" required:"create" doc:"Name, written for the locale."`
	Description *string `json:"description" doc:"Description, written for the locale."`
	Encoding    *string `json:"encoding" doc:"Encoding of the data."`
	Data        *string `json:"data" doc:"The content, written for the locale."`
	Sequence    *int    `json:"sequence" doc:"Order among the siblings."`
	LocaleCode  *string `json:"locale_code" doc:"Code of the locale the texts are written for; the session's own when absent."`
}

// PatchInput is the body of PATCH /resources/{id}: the keys of Input, none required.
type PatchInput struct {
	Root        *string `json:"root" format:"uuid" doc:"Root of the tree."`
	Node        *string `json:"node" format:"uuid" doc:"The node above."`
	Type        *string `json:"type" doc:"Media type of the content."`
	Name        *string `json:"name" doc:"Name, written for the locale. Not empty."`
	Description *string `json:"description" doc:"Description, written for the locale."`
	Encoding    *string `json:"encoding" doc:"Encoding of the data."`
	Data        *string `json:"data" doc:"The content, written for the locale."`
	Sequence    *int    `json:"sequence" doc:"Order among the siblings."`
	LocaleCode  *string `json:"locale_code" doc:"Code of the locale the texts are written for; the session's own when absent."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	p := resources.Prefix
	row := openapi.Schema{Name: "Resource", Value: Row{}, Mode: openapi.Row,
		Description: "A row of api.resource: a node of the tree of resources with its content in the session's locale. Every property is present, a property that has no value is null. In a list answered with `fields`, only the named properties are present."}
	in := openapi.Schema{Name: "ResourceInput", Value: Input{}, Mode: openapi.Create,
		Description: "Body of POST " + p + ". An absent key and an explicit null both mean \"not set\", except for a required key: it must be present and not empty (400 otherwise); a key the schema does not name is refused with 400."}
	patch := openapi.Schema{Name: "ResourcePatch", Value: PatchInput{}, Mode: openapi.Create,
		Description: "Body of PATCH " + p + "/{id}. An absent key and an explicit null both mean \"do not change\"; a key the schema does not name is refused with 400."}
	etag := openapi.Header{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}
	id := openapi.PathID("id", "Identifier of the resource.")
	admin := " Reading is open to any session; writing is the administrators' alone."
	return []openapi.Doc{
		{Method: "GET", Path: p, ID: "resources_list", Tag: "resources",
			Summary:     "List resources",
			Description: "The resources of the tree with their content in the session's locale, filtered, sorted and paged (api.list_resource and api.count_resource). total counts the rows of the same selection before paging." + admin,
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(row)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		{Method: "GET", Path: p + "/{id}", ID: "resources_get", Tag: "resources",
			Summary:     "Get a resource",
			Description: "One resource in the session's locale, api.get_resource. The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{id, {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK:       []openapi.Response{{Status: "200", Description: "The resource.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The id is not a UUID.",
			NotFound: "There is no such resource, or it has no content in the session's locale.",
			Extra:    []openapi.Response{{Status: "304", Description: "The resource has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}}}},
		{Method: "POST", Path: p, ID: "resources_create", Tag: "resources",
			Summary:     "Create a resource",
			Description: "api.set_resource with no id. The texts are written for locale_code, or for the session's locale when it is absent." + admin + " One request is one transaction; the answer is the new resource.",
			Params: []openapi.Param{{Name: "Idempotency-Key", In: "header", Schema: openapi.String,
				Description: "Optional. The same key with the same body replays the stored answer (marked Idempotency-Replayed: true) for 24 hours; the same key with another body is 409. A key is scoped to the session and the path; a 5xx answer is not stored. The store is in the memory of one service process: a replay is not found after a restart, nor on another instance."}},
			Body: &openapi.Body{Required: true, Schema: in},
			OK: []openapi.Response{{Status: "201", Description: "Created.", Schema: row, Headers: []openapi.Header{
				{Name: "Location", Description: "Path of the new resource.", Schema: openapi.String}, etag}}},
			Bad:   "No body, an unknown key, a value of the wrong type, a missing or empty name, or a value the database refuses (a locale code it does not know).",
			Extra: []openapi.Response{{Status: "409", Problem: true, Description: "Idempotency-Key reused with a different body, or the database reports a conflict."}}},
		{Method: "PATCH", Path: p + "/{id}", ID: "resources_update", Tag: "resources",
			Summary:     "Change a resource",
			Description: "api.set_resource with the id: the given keys change. If-Match is required: the ETag of the revision the client read." + admin,
			Params: []openapi.Param{id, {Name: "If-Match", In: "header", Required: true, Schema: openapi.String,
				Description: "The ETag of the row as read; * matches any current row. Without it the answer is 428 and the body is not read."}},
			Body:     &openapi.Body{Required: true, Schema: patch},
			OK:       []openapi.Response{{Status: "200", Description: "The resource after the change.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The id is not a UUID, an unknown key, a value of the wrong type, an empty name, or a value the database refuses.",
			NotFound: "There is no such resource, or it has no content in the session's locale.",
			Extra: []openapi.Response{
				{Status: "412", Problem: true, Description: "The row changed since it was read: If-Match is no longer its ETag."},
				{Status: "428", Problem: true, Description: "No If-Match header."}}},
		{Method: "DELETE", Path: p + "/{id}", ID: "resources_delete", Tag: "resources",
			Summary:     "Delete a resource",
			Description: "api.delete_resource, on a resource the caller can read." + admin,
			Params:      []openapi.Param{id},
			OK:          []openapi.Response{{Status: "204", Description: "Deleted. No body."}},
			Bad:         "The id is not a UUID.",
			NotFound:    "There is no such resource, or it has no content in the session's locale."},
	}
}
