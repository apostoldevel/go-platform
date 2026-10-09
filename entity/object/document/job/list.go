package job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/query"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// This file repeats, hand-written, what configuration/csms/lib/entity gives
// the project's own entity packages — the list with q=, the action, the
// methods — which the module boundary forbids importing. The search jsonb
// layout is api.sql()'s operator language; the arrangement below mirrors
// lib/entity's search.go and stays in the database's terms.

// searchFields are the columns of api.job one term of q= is matched against
// (kernel.ObjectJob flattens both to text columns, ILIKE applies).
var searchFields = []string{"code", "label"}

// knownKeys refuses a query parameter the route does not take: an unknown
// top-level key is a typo ("limit=10" of "page[limit]=10"), not a request
// to ignore.
func knownKeys(values url.Values, plain ...string) error {
	for k := range values {
		if strings.HasPrefix(k, "filter[") || strings.HasPrefix(k, "page[") {
			continue
		}
		known := false
		for _, p := range plain {
			known = known || k == p
		}
		if !known {
			return problem.New(400, "validation", "Bad request", "unknown parameter "+k)
		}
	}
	return nil
}

// condition is one element of api.sql()'s search array. lib/query's
// Condition has no place for condition/lstr/rstr — the OR group needs them.
type condition struct {
	Condition string   `json:"condition,omitempty"`
	Field     string   `json:"field"`
	Compare   string   `json:"compare,omitempty"`
	Value     string   `json:"value,omitempty"`
	ValArr    []string `json:"valarr,omitempty"`
	LStr      string   `json:"lstr,omitempty"`
	RStr      string   `json:"rstr,omitempty"`
}

// searchOf is the search jsonb of a list request: the filter[…] conditions
// lib/query translated (AND), then, when q= carries a term, one bracketed
// group `( f1 ILIKE %q% OR f2 ILIKE %q% … )` over the search fields. nil
// when there is nothing.
func searchOf(p query.Params, q string) []byte {
	term := strings.TrimSpace(q)
	if len(p.Search) == 0 && term == "" {
		return nil
	}
	out := make([]condition, 0, len(p.Search)+len(searchFields))
	for _, c := range p.Search {
		out = append(out, condition{Field: c.Field, Compare: c.Compare, Value: c.Value, ValArr: c.ValArr})
	}
	if term != "" {
		value := "%" + escapeLike(term) + "%"
		for i, f := range searchFields {
			c := condition{Field: f, Compare: "IKE", Value: value}
			if i > 0 {
				c.Condition = "OR"
			}
			if i == 0 {
				c.LStr = "("
			}
			if i == len(searchFields)-1 {
				c.RStr = ")"
			}
			out = append(out, c)
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// escapeLike makes the term literal under LIKE/ILIKE (PostgreSQL's default
// escape character is the backslash).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// list is GET /jobs: filter[…]/sort/fields/page as lib/query translates
// them, plus q= — answered as {items, total, limit, offset}, total from
// api.count_job over the same search. A query parameter the route does not
// know is 400.
func (m *module) list(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	if err := knownKeys(values, "sort", "fields", "q"); err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	// q= is a term search, not the list language's parameter: taken here,
	// removed before Parse — with it left in, an unknown a-z name is a
	// client's typo and Parse refuses it
	term := values.Get("q")
	values.Del("q")
	params, err := query.Parse(values)
	if err != nil {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", err.Error()))
		return
	}
	search := searchOf(params, term)
	_, orderby, _ := params.JSONArgs()
	var items []json.RawMessage
	var total int64
	qp, _ := json.Marshal(map[string]any{"query": r.URL.RawQuery})
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, qp), func(ctx context.Context, tx pgx.Tx) error {
		if err := pgtx.CallScalar(ctx, tx, m.resource.CountFn, pgtx.Args{"search": pgtx.JSON(search)}, &total); err != nil {
			return err
		}
		rows, err := pgtx.Call(ctx, tx, m.resource.ListFn, pgtx.Args{"search": pgtx.JSON(search), "limit": params.Limit, "offset": params.Offset, "orderby": pgtx.JSON(orderby)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			items = append(items, rest.Project(row, params.Fields))
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	body, _ := json.Marshal(map[string]any{"items": items, "total": total, "limit": params.Limit, "offset": params.Offset})
	rest.WriteJSON(w, 200, body)
}

var actionRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// action is POST /jobs/{id}/actions/{action}: the workflow's
// api.execute_object_action by code, on a visible row (404 otherwise); the
// body, when present, is the action's params; the answer is the row after.
func (m *module) action(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	action := r.PathValue("action")
	if !actionRe.MatchString(action) {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "bad action name"))
		return
	}
	params, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		rest.Fail(w, r, m.log, problem.New(413, "validation", "Payload too large", "body over 1 MiB"))
		return
	case err != nil, len(params) > 0 && (!json.Valid(params) || bytes.TrimLeft(params, " \t\r\n")[0] != '{'):
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "body must be a JSON object or empty"))
		return
	}
	var row json.RawMessage
	req := rest.ReqOf(r, 200, params)
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), req, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := m.resource.GetRow(ctx, tx, id); err != nil {
			return err
		}
		if _, err := pgtx.Call(ctx, tx, "execute_object_action", pgtx.Args{"object": id, "code": action, "params": pgtx.JSON(params)}); err != nil {
			return err
		}
		// the row after the action is the answer; drop leaves none — 204
		// then, decided here so that api.log_request records the status on
		// the wire
		var err error
		if row, err = m.resource.GetRow(ctx, tx, id); err != nil {
			var p *problem.Problem
			if !errors.As(err, &p) || p.Status != 404 {
				return err
			}
			row, req.Status = nil, 204
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if req.Status == 204 {
		w.WriteHeader(204)
		return
	}
	rest.SetETag(w, row)
	rest.WriteJSON(w, 200, row)
}

// methods is GET /jobs/{id}/methods: api.get_object_methods of a visible
// row, in the order the workflow gives them. Only fields= is taken — a
// sort/filter/page would be validated and then ignored, so it is refused.
func (m *module) methods(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	for k := range r.URL.Query() {
		if k != "fields" {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "only fields= is taken here, not "+k))
			return
		}
	}
	fields, err := query.Parse(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", err.Error()))
		return
	}
	var items []json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := m.resource.GetRow(ctx, tx, id); err != nil {
			return err
		}
		rows, err := pgtx.Call(ctx, tx, "get_object_methods", pgtx.Args{"object": id})
		if err != nil {
			return err
		}
		for _, row := range rows {
			items = append(items, rest.Project(row, fields.Fields))
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	body, _ := json.Marshal(items)
	rest.WriteJSON(w, 200, body)
}

func idArg(r *http.Request) (pgtx.Args, error) {
	id, err := rest.IDOf(r)
	if err != nil {
		return nil, err
	}
	return pgtx.Args{"id": id}, nil
}
