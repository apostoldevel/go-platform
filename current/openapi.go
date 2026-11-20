package current

import (
	"encoding/json"

	"github.com/apostoldevel/go-platform/lib/openapi"
)

// User is a row of the view api.users as api.current_user() gives it: the
// caller's own account. Every column but the id is a pointer (nullable): a
// view does not say which can be NULL.
type User struct {
	ID                string  `json:"id" format:"uuid" doc:"Identifier of the user."`
	Username          *string `json:"username" doc:"Login."`
	Name              *string `json:"name" doc:"Display name."`
	GivenName         *string `json:"given_name" doc:"Given name."`
	FamilyName        *string `json:"family_name" doc:"Family name."`
	PatronymicName    *string `json:"patronymic_name" doc:"Patronymic."`
	Email             *string `json:"email" doc:"E-mail address."`
	EmailVerified     *bool   `json:"email_verified" doc:"The e-mail address is verified."`
	Phone             *string `json:"phone" doc:"Phone number."`
	PhoneVerified     *bool   `json:"phone_verified" doc:"The phone number is verified."`
	SessionLimit      *int32  `json:"session_limit" doc:"Number of sessions the user may hold at once."`
	Created           *string `json:"created" format:"date-time" doc:"Time the account was created."`
	Locale            *string `json:"locale" format:"uuid" doc:"Default locale of the account."`
	LocaleCode        *string `json:"locale_code" doc:"Code of that locale."`
	Area              *string `json:"area" format:"uuid" doc:"Default area of the account."`
	AreaCode          *string `json:"area_code" doc:"Code of that area."`
	Interface         *string `json:"interface" format:"uuid" doc:"Default interface of the account."`
	InterfaceCode     *string `json:"interface_code" doc:"Code of that interface."`
	Description       *string `json:"description" doc:"Description."`
	Picture           *string `json:"picture" doc:"Picture."`
	PasswordChange    *bool   `json:"passwordchange" doc:"The user must change the password."`
	PasswordNotChange *bool   `json:"passwordnotchange" doc:"The user may not change the password."`
	System            *bool   `json:"system" doc:"A system account."`
	ReadOnly          *bool   `json:"readonly" doc:"The account is read-only."`
	Status            *int32  `json:"status" doc:"Status bits of the account."`
	StatusText        *string `json:"statustext" doc:"Status as text."`
	State             *int32  `json:"state" doc:"State of the account."`
	StateText         *string `json:"statetext" doc:"State as text."`
	LockDate          *string `json:"lock_date" format:"date-time" doc:"Time the account is locked from."`
	ExpiryDate        *string `json:"expiry_date" format:"date-time" doc:"Time the account expires."`
	LcIP              *string `json:"lc_ip" doc:"Address of the last login."`
	InputCount        *int32  `json:"input_count" doc:"Number of logins."`
	InputLast         *string `json:"input_last" format:"date-time" doc:"Time of the last login."`
	InputError        *int32  `json:"input_error" doc:"Consecutive failed logins."`
	InputErrorLast    *string `json:"input_error_last" format:"date-time" doc:"Time of the last failed login."`
	InputErrorAll     *int32  `json:"input_error_all" doc:"Failed logins in all."`
	Scope             *string `json:"scope" format:"uuid" doc:"Scope of the session."`
	ScopeCode         *string `json:"scope_code" doc:"Code of the scope."`
	ScopeName         *string `json:"scope_name" doc:"Name of the scope."`
	ScopeDescription  *string `json:"scope_description" doc:"Description of the scope."`
}

// Area is a row of the view kernel.area as api.current_area() gives it: the
// columns of api.area but its sort list.
type Area struct {
	ID               string  `json:"id" format:"uuid" doc:"Identifier of the area."`
	Parent           *string `json:"parent" format:"uuid" doc:"Parent area, or null."`
	Level            *int32  `json:"level" doc:"Depth in the tree of areas."`
	Sequence         *int32  `json:"sequence" doc:"Order among the siblings."`
	Type             *string `json:"type" format:"uuid" doc:"Type of the area."`
	TypeCode         *string `json:"typecode" doc:"Code of the type."`
	TypeName         *string `json:"typename" doc:"Name of the type."`
	Scope            *string `json:"scope" format:"uuid" doc:"Scope of the area."`
	ScopeCode        *string `json:"scopecode" doc:"Code of the scope."`
	ScopeName        *string `json:"scopename" doc:"Name of the scope."`
	ScopeDescription *string `json:"scopedescription" doc:"Description of the scope."`
	Code             *string `json:"code" doc:"Code of the area."`
	Name             *string `json:"name" doc:"Name of the area."`
	Description      *string `json:"description" doc:"Description of the area."`
	ValidFromDate    *string `json:"validfromdate" format:"date-time" doc:"The area is valid from this time."`
	ValidToDate      *string `json:"validtodate" format:"date-time" doc:"The area is valid until this time."`
}

// Interface is a row of the view api.interface as api.current_interface() gives it.
type Interface struct {
	ID          string  `json:"id" format:"uuid" doc:"Identifier of the interface."`
	Code        *string `json:"code" doc:"Code of the interface."`
	Name        *string `json:"name" doc:"Name of the interface."`
	Description *string `json:"description" doc:"Description of the interface."`
}

// Locale is a row of the view api.locale as api.current_locale() gives it.
type Locale struct {
	ID          string  `json:"id" format:"uuid" doc:"Identifier of the locale."`
	Code        *string `json:"code" doc:"Code of the locale."`
	Name        *string `json:"name" doc:"Name of the locale."`
	Description *string `json:"description" doc:"Description of the locale."`
}

// Me is the answer of GET and PATCH /me: the session's state in one object.
type Me struct {
	User      *User      `json:"user" doc:"The caller's account."`
	Area      *Area      `json:"area" doc:"The area of the session, or null where there is none."`
	Interface *Interface `json:"interface" doc:"The interface of the session, or null where there is none."`
	Locale    *Locale    `json:"locale" doc:"The locale of the session, or null where there is none."`
	OperDate  *string    `json:"oper_date" format:"date-time" doc:"The operating date of the session, or null where it is not set."`
}

// Input is the body of PATCH /me.
type Input struct {
	Area      *string         `json:"area" doc:"Area of the session: a UUID or a code."`
	Interface *string         `json:"interface" doc:"Interface of the session: a UUID; a code is refused."`
	Locale    *string         `json:"locale" doc:"Locale of the session: a UUID or a code."`
	OperDate  json.RawMessage `json:"oper_date" format:"date-time" doc:"Operating date of the session: an RFC 3339 timestamp, or null to clear it."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

func (m *module) docs() []openapi.Doc {
	me := openapi.Schema{Name: "Me", Value: Me{}, Mode: openapi.Row,
		Description: "The state of the caller's session: the account, the area, the interface and the locale it works in, and the operating date. Each part is a row of the view it names, or null where the session has none."}
	in := openapi.Schema{Name: "MePatch", Value: Input{}, Mode: openapi.Create,
		Description: "Body of PATCH /me. At least one key is required; a key the schema does not name is refused with 400."}
	return []openapi.Doc{
		{Method: "GET", Path: prefix, ID: "me_get", Tag: "me",
			Summary:     "The caller's session",
			Description: "The account, the area, the interface and the locale of the session, and its operating date, in one object (api.current_user, api.current_area, api.current_interface, api.current_locale, api.oper_date). The session itself is known from the token; its code is never on the wire.",
			OK:          openapi.OK200("The session.", me)},
		{Method: "PATCH", Path: prefix, ID: "me_update", Tag: "me",
			Summary:     "Change the caller's session",
			Description: "Sets the given keys of the session, each by its api.set_session_<key> in order area, interface, locale, oper_date, in one transaction; the answer is the same object GET gives. Keys that are absent stay as they are. The area and the locale are given by UUID or code; the interface by UUID only.",
			Body:        &openapi.Body{Required: true, Schema: in},
			OK:          openapi.OK200("The session after the change.", me),
			Bad:         "No body, or a body with none of area, interface, locale, oper_date; an unknown key; area, interface or locale empty; interface that is not a UUID; oper_date that is not an RFC 3339 timestamp or null; or a locale or an area the database does not know."},
	}
}
