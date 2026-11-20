package openapi

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// date-time of RFC 3339, section 5.6: the T, the seconds and the offset are required
	timeRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$`)
)

// Conforms lists how a JSON value differs from a schema node of a document
// built by Build — the instrument that holds the document to the answers a
// real stack gives: a property the schema does not name, a
// required property that is missing, a value of another type, a string that is
// not of its format, a value outside the enum. An empty list is a fit. The value
// is what json.Unmarshal (or a decoder with UseNumber) gives; schema is the
// node of the document (a map), $ref resolved against doc.
//
// It reads the subset Build writes (type incl. a list with "null", properties,
// required, items, additionalProperties as a schema, enum, format uuid and
// date-time, oneOf, $ref); a keyword it does not know is ignored, not guessed.
func Conforms(doc map[string]any, schema any, value any) []string {
	var out []string
	conform(doc, schema, value, "$", &out)
	sort.Strings(out)
	return out
}

func conform(doc map[string]any, schema any, v any, at string, out *[]string) {
	s, ok := schema.(map[string]any)
	if !ok {
		return // true / absent schema: anything
	}
	if ref, ok := s["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/components/schemas/")
		comps, _ := doc["components"].(map[string]any)
		schemas, _ := comps["schemas"].(map[string]any)
		target, ok := schemas[name]
		if !found || !ok {
			*out = append(*out, at+": $ref "+ref+" does not resolve")
			return
		}
		conform(doc, target, v, at, out)
		return
	}
	if alts, ok := s["oneOf"].([]any); ok {
		for _, a := range alts {
			var sub []string
			conform(doc, a, v, at, &sub)
			if len(sub) == 0 {
				return
			}
		}
		*out = append(*out, at+": the value fits none of the alternatives")
		return
	}
	var types []string
	switch t := s["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, e := range t {
			types = append(types, e.(string))
		}
	}
	if len(types) > 0 {
		kind := kindOf(v)
		fit := false
		for _, t := range types {
			if t == kind || (t == "number" && kind == "integer") {
				fit = true
			}
		}
		if !fit {
			*out = append(*out, fmt.Sprintf("%s: is %s, the schema says %s", at, kind, strings.Join(types, "|")))
			return
		}
	}
	if enum, ok := s["enum"].([]any); ok && v != nil {
		found := false
		for _, e := range enum {
			if sameValue(e, v) {
				found = true
			}
		}
		if !found {
			*out = append(*out, fmt.Sprintf("%s: %v is not in the enum", at, v))
		}
	}
	switch x := v.(type) {
	case string:
		switch s["format"] {
		case "uuid":
			if !uuidRe.MatchString(x) {
				*out = append(*out, fmt.Sprintf("%s: %q is not a uuid", at, x))
			}
		case "date-time":
			if !timeRe.MatchString(x) {
				*out = append(*out, fmt.Sprintf("%s: %q is not a date-time", at, x))
			}
		}
	case []any:
		for i, e := range x {
			conform(doc, s["items"], e, fmt.Sprintf("%s[%d]", at, i), out)
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		extra, hasExtra := s["additionalProperties"]
		for k, e := range x {
			if p, ok := props[k]; ok {
				conform(doc, p, e, at+"."+k, out)
				continue
			}
			switch {
			case hasExtra && extra == false:
				*out = append(*out, at+": has "+k+", the schema forbids it")
			case hasExtra:
				conform(doc, extra, e, at+"."+k, out)
			case props != nil:
				*out = append(*out, at+": has "+k+", the schema does not name it")
			}
		}
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := x[r.(string)]; !ok {
					*out = append(*out, at+": lacks "+r.(string)+", which the schema requires")
				}
			}
		}
	}
}

func kindOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if !math.IsInf(x, 0) && !math.IsNaN(x) && x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

// sameValue compares an enum member with a value of the same JSON kind: the
// string "1" is not the number 1.
func sameValue(a, b any) bool {
	if kindOf(a) != kindOf(b) && !(isNumber(a) && isNumber(b)) {
		return false
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func isNumber(v any) bool {
	k := kindOf(v)
	return k == "integer" || k == "number"
}
