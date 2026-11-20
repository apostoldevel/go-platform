package admin

import (
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Locale is a row of the view api.locale: a language of the texts.
type Locale struct {
	ID          string  `json:"id" format:"uuid" doc:"Identifier of the locale."`
	Code        string  `json:"code" doc:"Code of the locale."`
	Name        string  `json:"name" doc:"Name of the locale."`
	Description *string `json:"description" doc:"Description of the locale."`
}

// Operations is the module's description of its routes: the locales only. The
// other routes of the module are the administrators' and are not described.
func (m *module) Operations() []openapi.Operation {
	return openapi.Docs(openapi.Doc{
		Method: "GET", Path: locales.Prefix, ID: "locales_list", Tag: "locales",
		Summary:     "List locales",
		Description: "The languages the texts exist in, by code (api.list_locale), not paged.",
		OK: openapi.OK200("The locales.", openapi.Array(openapi.Schema{Name: "Locale", Value: Locale{}, Mode: openapi.Row,
			Description: "A row of api.locale."})),
	})
}

var _ openapi.Documented = (*module)(nil)
