package kladr

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Node is a row of the view api.address_tree: a node of the address classifier.
type Node struct {
	ID     int32   `json:"id" doc:"Identifier of the node."`
	Parent *int32  `json:"parent" doc:"The node above, or null at the top."`
	Code   string  `json:"code" doc:"KLADR code of the node."`
	Name   string  `json:"name" doc:"Name of the place."`
	Short  *string `json:"short" doc:"Abbreviated kind of the place, such as a street or a city."`
	Index  *string `json:"index" doc:"Postal index, or null."`
	Level  int32   `json:"level" doc:"Level of the node in the tree."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	p := nodes.Prefix
	node := openapi.Schema{Name: "Kladr", Value: Node{}, Mode: openapi.Row,
		Description: "A node of the address classifier, a row of api.address_tree."}
	id := openapi.PathInt("id", "Identifier of the node.")
	idBad := "The id is not a positive integer, or is above 2147483647."
	return []openapi.Doc{
		{Method: "GET", Path: p, ID: "kladr_list", Tag: "kladr",
			Summary:     "List nodes of the address classifier",
			Description: "The nodes of the classifier, filtered, sorted and paged (api.list_address_tree and api.count_address_tree). total counts the rows of the same selection before paging.",
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(node)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		{Method: "GET", Path: p + "/string", ID: "kladr_string", Tag: "kladr",
			Summary:     "A KLADR code as an address line",
			Description: "The address line of the node with the code, its name and those of the nodes above it, the highest first, joined with commas (api.get_address_tree_string). `short` chooses how the abbreviated kind is written; `level` stops the climb at nodes below that level. An unknown code gives a null address. The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{
				openapi.Q("code", openapi.String, true, "KLADR code: 1 to 20 digits."),
				openapi.Q("short", map[string]any{"type": "integer", "minimum": 0, "default": 0}, false, "0: the name only; 1: kind, a dot, then the name; any other value: the name, then the kind and a dot. Not above 2147483647."),
				openapi.Q("level", map[string]any{"type": "integer", "minimum": 0, "default": 0}, false, "Lowest level of node to include: the climb stops below it. With 0 every level is included, and the postal index of the node of level 5, where it has one, is put first. Not above 2147483647."),
			},
			OK: []openapi.Response{{Status: "200", Description: "The address line.", Schema: openapi.Obj("The address line.", map[string]any{
				"address": openapi.Nullable("string", ""),
			}), Headers: []openapi.Header{{Name: "ETag", Description: "Weak ETag of the answer: a digest of it.", Schema: openapi.String}}}},
			Bad:   "The code is absent or is not 1 to 20 digits, or short or level is not a non-negative integer that fits 32 bits.",
			Extra: []openapi.Response{{Status: "304", Description: "The answer has not changed since If-None-Match. No body."}}},
		{Method: "GET", Path: p + "/{id}", ID: "kladr_get", Tag: "kladr",
			Summary:     "Get a node of the address classifier",
			Description: "One node, api.get_address_tree. The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{id, {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK:       []openapi.Response{{Status: "200", Description: "The node.", Schema: node, Headers: []openapi.Header{{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}}}},
			Bad:      idBad,
			NotFound: "There is no such node.",
			Extra:    []openapi.Response{{Status: "304", Description: "The node has not changed since If-None-Match. No body."}}},
		{Method: "GET", Path: p + "/{id}/history", ID: "kladr_history", Tag: "kladr",
			Summary:     "The path of a node to the root",
			Description: "The node itself and every node above it up to the top of the tree, the node first (api.get_address_tree_history). Not paged; an unknown id gives an empty array.",
			Params:      []openapi.Param{id},
			OK:          openapi.OK200("The nodes.", openapi.Array(node)),
			Bad:         idBad},
	}
}
