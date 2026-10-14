package entity

import (
	"encoding/json"
	"strings"

	"github.com/apostoldevel/go-platform/lib/query"
)

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

// SearchOf is the search jsonb of a list request: the filter[…] conditions
// lib/query translated (AND), then, when q= carries a term, one bracketed
// group `( f1 ILIKE %q% OR f2 ILIKE %q% … )` over fields — the operator
// language stays in the database, this only arranges the array. nil when
// there is nothing; a term with no fields to match is nothing too.
func SearchOf(p query.Params, q string, fields []string) []byte {
	term := strings.TrimSpace(q)
	if len(fields) == 0 {
		term = ""
	}
	if len(p.Search) == 0 && term == "" {
		return nil
	}
	out := make([]condition, 0, len(p.Search)+len(fields))
	for _, c := range p.Search {
		out = append(out, condition{Field: c.Field, Compare: c.Compare, Value: c.Value, ValArr: c.ValArr})
	}
	if term != "" {
		value := "%" + EscapeLike(term) + "%"
		for i, f := range fields {
			c := condition{Field: f, Compare: "IKE", Value: value}
			if i > 0 {
				c.Condition = "OR"
			}
			if i == 0 {
				c.LStr = "("
			}
			if i == len(fields)-1 {
				c.RStr = ")"
			}
			out = append(out, c)
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// EscapeLike makes the term literal under LIKE/ILIKE (PostgreSQL's default
// escape character is the backslash).
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
