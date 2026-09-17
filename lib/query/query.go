// Package query translates the list parameters of /api/v2
//
//	?filter[state]=enabled&filter[created][gte]=…&filter[state][in]=a,b
//	&sort=-created,name&fields=id,name&page[limit]=50&page[offset]=100
//
// into the search/orderby/fields jsonb of api.sql() (db-platform).
// The operator language stays in the database; this package only renames.
package query

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultLimit is api.sql()'s own default page size.
const DefaultLimit = 500

// MaxLimit bounds page[limit].
const MaxLimit = 1000

// Condition is one element of the search array.
type Condition struct {
	Field   string   `json:"field"`
	Compare string   `json:"compare,omitempty"`
	Value   string   `json:"value,omitempty"`
	ValArr  []string `json:"valarr,omitempty"`
}

// Params is the translated request.
type Params struct {
	Search  []Condition
	OrderBy []string
	Fields  []string
	Limit   int
	Offset  int
}

var (
	ident = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	key   = regexp.MustCompile(`^(filter|page)\[([^\]]+)\](?:\[([^\]]+)\])?$`)
)

var operators = map[string]string{
	"eq": "EQL", "ne": "NEQ", "lt": "LSS", "lte": "LEQ", "gt": "GTR", "gte": "GEQ",
	"like": "LKE", "ilike": "IKE", "in": "", "null": "",
}

// Parse validates and translates. Unknown top-level parameters are ignored;
// a malformed known one is an error (HTTP 400 for the caller).
func Parse(v url.Values) (Params, error) {
	p := Params{Limit: DefaultLimit}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys) // url.Values is a map; make the translation deterministic
	for _, k := range keys {
		vals := v[k]
		val := vals[len(vals)-1]
		switch {
		case k == "sort":
			for _, f := range strings.Split(val, ",") {
				dir := "ASC"
				if strings.HasPrefix(f, "-") {
					dir, f = "DESC", f[1:]
				}
				if !ident.MatchString(f) {
					return p, fmt.Errorf("sort: bad field %q", f)
				}
				p.OrderBy = append(p.OrderBy, f+" "+dir)
			}
		case k == "fields":
			for _, f := range strings.Split(val, ",") {
				if !ident.MatchString(f) {
					return p, fmt.Errorf("fields: bad field %q", f)
				}
				p.Fields = append(p.Fields, f)
			}
		case k == "filter" || k == "page":
			return p, fmt.Errorf("%s needs a key: %s[…]", k, k)
		case strings.HasPrefix(k, "filter[") || strings.HasPrefix(k, "page["):
			m := key.FindStringSubmatch(k)
			if m == nil {
				return p, fmt.Errorf("bad parameter %q", k)
			}
			if m[1] == "page" {
				if err := p.page(m[2], val); err != nil {
					return p, err
				}
				continue
			}
			c, err := condition(m[2], m[3], val)
			if err != nil {
				return p, err
			}
			p.Search = append(p.Search, c)
		}
	}
	return p, nil
}

func (p *Params) page(k, val string) error {
	n, err := strconv.Atoi(val)
	switch k {
	case "limit":
		if err != nil || n < 1 || n > MaxLimit {
			return fmt.Errorf("page[limit]: 1..%d", MaxLimit)
		}
		p.Limit = n
	case "offset":
		if err != nil || n < 0 {
			return fmt.Errorf("page[offset]: ≥ 0")
		}
		p.Offset = n
	default:
		return fmt.Errorf("page[%s]: not supported (use page[limit], page[offset])", k)
	}
	return nil
}

func condition(field, op, val string) (Condition, error) {
	if !ident.MatchString(field) {
		return Condition{}, fmt.Errorf("filter: bad field %q", field)
	}
	if op == "" {
		op = "eq"
	}
	cmp, ok := operators[op]
	if !ok {
		return Condition{}, fmt.Errorf("filter[%s]: unknown operator %q", field, op)
	}
	switch op {
	case "in":
		return Condition{Field: field, ValArr: strings.Split(val, ",")}, nil
	case "null":
		if val == "true" {
			return Condition{Field: field, Compare: "ISN"}, nil
		}
		return Condition{Field: field, Compare: "INN"}, nil
	}
	return Condition{Field: field, Compare: cmp, Value: val}, nil
}

// JSONArgs returns search, orderby and fields as jsonb arguments — nil (SQL
// NULL) when empty, so api.sql() applies its defaults.
func (p Params) JSONArgs() (search, orderby, fields []byte) {
	if len(p.Search) > 0 {
		search, _ = json.Marshal(p.Search)
	}
	if len(p.OrderBy) > 0 {
		orderby, _ = json.Marshal(p.OrderBy)
	}
	if len(p.Fields) > 0 {
		fields, _ = json.Marshal(p.Fields)
	}
	return
}
