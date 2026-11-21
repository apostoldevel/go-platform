package log

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// UserLogRow is a row of the view api.user_log: one entry of the event log, as
// the database shows it to the caller. Every column but the id is a pointer
// (nullable): a view does not say which can be NULL.
type UserLogRow struct {
	ID        int64   `json:"id" doc:"Identifier of the entry."`
	Type      *string `json:"type" doc:"Type of the entry: M message, W warning, E error, D debug." enum:"M,W,E,D"`
	TypeName  *string `json:"typename" doc:"Name of the type."`
	Datetime  *string `json:"datetime" format:"date-time" doc:"Operating time of the entry."`
	Timestamp *string `json:"timestamp" format:"date-time" doc:"Time the entry was written."`
	Username  *string `json:"username" doc:"Login of the user the entry was written for."`
	Session   *string `json:"session" doc:"Code of the session the entry was written in; null where the database withholds it (a configuration may give no session code in a journal)."`
	Code      *int32  `json:"code" doc:"Numeric code of the event."`
	Scope     *string `json:"scope" doc:"Area of the system the event belongs to."`
	Event     *string `json:"event" doc:"Name of the event."`
	Text      *string `json:"text" doc:"Text of the entry."`
	Category  *string `json:"category" doc:"Category of the entry."`
	Object    *string `json:"object" format:"uuid" doc:"The object the entry is about, or null."`
}

// Operations is the module's description of its routes: the caller's own
// journal. The whole log (/event-log) is the administrators' and is not described.
func (m *module) Operations() []openapi.Operation {
	p := myLog.Prefix
	row := openapi.Schema{Name: "UserLog", Value: UserLogRow{}, Mode: openapi.Row,
		Description: "A row of api.user_log: an entry of the event log. Every property is present, a property that has no value is null. In a list answered with `fields`, only the named properties are present."}
	seen := " The database shows an entry to the user it was written for, and every entry to an administrator."
	return openapi.Docs(
		openapi.Doc{Method: "GET", Path: p, ID: "me_event_log_list", Tag: "me",
			Summary:     "List the caller's event log",
			Description: "The entries of the event log, filtered, sorted and paged (api.list_user_log and api.count_user_log). total counts the rows of the same selection before paging." + seen,
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(row)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		openapi.Doc{Method: "GET", Path: p + "/{id}", ID: "me_event_log_get", Tag: "me",
			Summary:     "Get an entry of the caller's event log",
			Description: "One entry, api.get_user_log." + seen + " The answer carries an ETag and honours If-None-Match.",
			Params: []openapi.Param{openapi.PathInt("id", "Identifier of the entry."), {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK: []openapi.Response{{Status: "200", Description: "The entry.", Schema: row, Headers: []openapi.Header{
				{Name: "ETag", Description: "Weak ETag of the row: a digest of it.", Schema: openapi.String}}}},
			Bad:      "The id is not a positive integer.",
			NotFound: "There is no such entry, or the database does not show it to the caller; the two are not told apart.",
			Extra: []openapi.Response{{Status: "304", Description: "The entry has not changed since If-None-Match. No body.",
				Headers: []openapi.Header{{Name: "ETag", Description: "Weak ETag of the row.", Schema: openapi.String}}}}},
	)
}

var _ openapi.Documented = (*module)(nil)
