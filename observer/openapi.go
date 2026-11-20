package observer

import (
	"encoding/json"

	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Publisher is a row of the view api.publisher: a source of events a session
// can subscribe to.
type Publisher struct {
	Code        string  `json:"code" doc:"Code of the publisher."`
	Name        string  `json:"name" doc:"Name of the publisher."`
	Description *string `json:"description" doc:"Description of the publisher."`
}

// Listener is a subscription of the caller's own session to a publisher. The
// session code the database keeps with it is never answered.
type Listener struct {
	Publisher string          `json:"publisher" doc:"Code of the publisher."`
	Identity  string          `json:"identity" doc:"Identity of the subscription, a channel name."`
	Filter    json.RawMessage `json:"filter" doc:"The filter of the subscription, a JSON value."`
	Params    json.RawMessage `json:"params" doc:"The parameters of the subscription, a JSON value."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	pub := openapi.Schema{Name: "ObserverPublisher", Value: Publisher{}, Mode: openapi.Row,
		Description: "A row of api.publisher."}
	lis := openapi.Schema{Name: "ObserverListener", Value: Listener{}, Mode: openapi.Row,
		Description: "A subscription of the caller's session to a publisher: api.list_my_listener and api.get_my_listener without the session code, which is a credential and never leaves the service."}
	code := "A code: a lower-case letter, then lower-case letters, digits and underscores; anything else is refused with 400."
	etag := openapi.Header{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}
	inm := openapi.Param{Name: "If-None-Match", In: "header", Schema: openapi.String,
		Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}
	subs := "Subscriptions live in a WebSocket session and are made there; this route reads, never writes."
	return []openapi.Doc{
		{Method: "GET", Path: publishers, ID: "observer_publishers_list", Tag: "observer",
			Summary:     "List publishers",
			Description: "Every publisher, by code (api.list_publisher), not paged.",
			OK:          openapi.OK200("The publishers.", openapi.Array(pub))},
		{Method: "GET", Path: publishers + "/{code}", ID: "observer_publishers_get", Tag: "observer",
			Summary:     "Get a publisher",
			Description: "One publisher by its code, api.get_publisher. The answer carries an ETag and honours If-None-Match.",
			Params:      []openapi.Param{openapi.PathText("code", "Code of the publisher. "+code), inm},
			OK:          []openapi.Response{{Status: "200", Description: "The publisher.", Schema: pub, Headers: []openapi.Header{etag}}},
			Bad:         "The code is malformed.",
			NotFound:    "There is no such publisher.",
			Extra:       []openapi.Response{{Status: "304", Description: "The publisher has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}}}},
		{Method: "GET", Path: prefix + "/listeners", ID: "observer_listeners_list", Tag: "observer",
			Summary:     "List the subscriptions of the caller's session",
			Description: "The subscriptions of the caller's own session, by publisher and identity, not paged. " + subs,
			OK:          openapi.OK200("The subscriptions.", openapi.Array(lis))},
		{Method: "GET", Path: prefix + "/listeners/{publisher}/{identity}", ID: "observer_listeners_get", Tag: "observer",
			Summary:     "Get a subscription of the caller's session",
			Description: "One subscription of the caller's own session (api.get_my_listener). " + subs + " The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{openapi.PathText("publisher", "Code of the publisher. "+code),
				openapi.PathText("identity", "Identity of the subscription. "+code), inm},
			OK:       []openapi.Response{{Status: "200", Description: "The subscription.", Schema: lis, Headers: []openapi.Header{etag}}},
			Bad:      "The publisher or the identity is malformed.",
			NotFound: "The caller's session has no such subscription.",
			Extra:    []openapi.Response{{Status: "304", Description: "The subscription has not changed since If-None-Match. No body.", Headers: []openapi.Header{etag}}}},
	}
}
