package notification

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Row is a row of the view api.my_notification: one notification of a change of
// state of an object. The identifiers and codes of what happened are always
// present; the old state is null where the object had none, the new one where
// it was left without.
type Row struct {
	ID               string  `json:"id" format:"uuid" doc:"Identifier of the notification."`
	Datetime         string  `json:"datetime" format:"date-time" doc:"Time of the notification."`
	UserID           string  `json:"userid" format:"uuid" doc:"The user who triggered the action; shown to every caller who may see the notification."`
	Object           string  `json:"object" format:"uuid" doc:"The object the notification is about."`
	Entity           string  `json:"entity" format:"uuid" doc:"Entity of the object."`
	EntityCode       string  `json:"entitycode" doc:"Code of the entity."`
	Class            string  `json:"class" format:"uuid" doc:"Class of the object."`
	ClassCode        string  `json:"classcode" doc:"Code of the class."`
	Action           string  `json:"action" format:"uuid" doc:"The action that was executed."`
	ActionCode       string  `json:"actioncode" doc:"Code of the action."`
	Method           string  `json:"method" format:"uuid" doc:"The method that was executed."`
	MethodCode       string  `json:"methodcode" doc:"Code of the method."`
	StateOld         *string `json:"stateold" format:"uuid" doc:"State the object left, or null."`
	StateOldType     *string `json:"stateoldtype" format:"uuid" doc:"Type of that state."`
	StateOldTypeCode *string `json:"stateoldtypecode" doc:"Code of the type of that state."`
	StateOldTypeName *string `json:"stateoldtypename" doc:"Name of the type of that state."`
	StateOldCode     *string `json:"stateoldcode" doc:"Code of that state."`
	StateOldLabel    *string `json:"stateoldlabel" doc:"Label of that state."`
	StateNew         *string `json:"statenew" format:"uuid" doc:"State the object came to, or null."`
	StateNewType     *string `json:"statenewtype" format:"uuid" doc:"Type of that state."`
	StateNewTypeCode *string `json:"statenewtypecode" doc:"Code of the type of that state."`
	StateNewTypeName *string `json:"statenewtypename" doc:"Name of the type of that state."`
	StateNewCode     *string `json:"statenewcode" doc:"Code of that state."`
	StateNewLabel    *string `json:"statenewlabel" doc:"Label of that state."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	p := notifications.Prefix
	note := openapi.Schema{Name: "Notification", Value: Row{}, Mode: openapi.Row,
		Description: "A row of api.my_notification: a change of state of an object, as the database shows it to the caller."}
	reads := "Every read is by the *_my_notification functions: the rows are those the database shows the caller, an administrator's being all of them."
	return []openapi.Doc{
		{Method: "GET", Path: p, ID: "notifications_list", Tag: "notifications",
			Summary:     "List notifications",
			Description: "The notifications the caller may see, filtered, sorted and paged (api.list_my_notification and api.count_my_notification). total counts the rows of the same selection before paging. " + reads,
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(note)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		{Method: "GET", Path: p + "/since", ID: "notifications_since", Tag: "notifications",
			Summary:     "Notifications since a time",
			Description: "The notifications from a point in time on (api.my_notification(from)), not paged. " + reads,
			Params:      []openapi.Param{openapi.Q("from", openapi.Time, false, "RFC 3339 time to read from, inclusive; the moment of the request when absent.")},
			OK:          openapi.OK200("The notifications.", openapi.Array(note)),
			Bad:         "from is not an RFC 3339 timestamp."},
		{Method: "GET", Path: p + "/changed", ID: "notifications_changed", Tag: "notifications",
			Summary:     "Objects changed in a period",
			Description: "The objects, among those named, that have notifications in the period: one row per distinct object, the row of its own entity's api.get_<entity>. An object the caller may not read is left out. The columns of a row are those of its entity, so the schema names only the id every row has. " + reads,
			Params: []openapi.Param{
				openapi.Q("objects", openapi.String, true, "Comma-separated UUIDs of the objects, from 1 to 100."),
				openapi.Q("from", openapi.Time, false, "RFC 3339 time the period starts at, inclusive; no lower bound when absent."),
				openapi.Q("to", openapi.Time, false, "RFC 3339 time the period ends at, exclusive; no upper bound when absent."),
			},
			OK: openapi.OK200("The objects.", openapi.Array(map[string]any{
				"type": "object", "required": []any{"id"}, "additionalProperties": true,
				"properties":  map[string]any{"id": openapi.UUIDNode},
				"description": "A row of the object's entity, as its own prefix answers it.",
			})),
			Bad: "objects is absent, holds an element that is not a UUID or more than 100, or from or to is not an RFC 3339 timestamp."},
		{Method: "GET", Path: p + "/{id}", ID: "notifications_get", Tag: "notifications",
			Summary:     "Get a notification",
			Description: "One notification, api.get_my_notification. " + reads + " The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{openapi.PathID("id", "Identifier of the notification."), {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK: []openapi.Response{{Status: "200", Description: "The notification.", Schema: note, Headers: []openapi.Header{
				{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}}}},
			Bad:      "The id is not a UUID.",
			NotFound: "There is no such notification, or the caller may not see it; the two are not told apart.",
			Extra: []openapi.Response{{Status: "304", Description: "The notification has not changed since If-None-Match. No body.",
				Headers: []openapi.Header{{Name: "ETag", Description: "Weak ETag of the row.", Schema: openapi.String}}}}},
	}
}
