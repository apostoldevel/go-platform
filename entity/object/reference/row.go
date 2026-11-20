package reference

// Row is a row of api.reference — and the columns every api.<x> of the
// reference family (agent, program, scheduler, vendor, version) starts with —
// in the names the database gives them (lower case). It is what GET
// /references and GET /references/{id} answer, and the schema of the OpenAPI
// document; the parity tool holds the document to real rows.
//
// A column is a pointer where the view can give NULL: the texts that come from
// a LEFT JOIN on the session's locale (names, labels, descriptions of the
// entity, class, type, state and state type), the object's parent, and the
// scope's description. Everything else comes from NOT NULL columns or inner
// joins.
type Row struct {
	ID               string  `json:"id" format:"uuid" doc:"Identifier of the row (the reference's own; the object has the same id in object)."`
	Object           string  `json:"object" format:"uuid" doc:"Identifier of the underlying object."`
	Parent           *string `json:"parent" format:"uuid" doc:"Parent object, or null."`
	Entity           string  `json:"entity" format:"uuid" doc:"Entity of the object."`
	EntityCode       string  `json:"entitycode" doc:"Code of the entity."`
	EntityName       *string `json:"entityname" doc:"Name of the entity in the session's locale, or null where it has no text."`
	Class            string  `json:"class" format:"uuid" doc:"Class of the object."`
	ClassCode        string  `json:"classcode" doc:"Code of the class."`
	ClassLabel       *string `json:"classlabel" doc:"Label of the class in the session's locale, or null."`
	Type             string  `json:"type" format:"uuid" doc:"Type of the row."`
	TypeCode         string  `json:"typecode" doc:"Code of the type, for example system.agent."`
	TypeName         *string `json:"typename" doc:"Name of the type in the session's locale, or null."`
	TypeDescription  *string `json:"typedescription" doc:"Description of the type in the session's locale, or null."`
	Code             string  `json:"code" doc:"Code of the reference: its business key, unique within the scope."`
	Name             *string `json:"name" doc:"Name of the row in the session's locale, or null where it has no text."`
	Label            *string `json:"label" doc:"Label of the object in the session's locale, or null."`
	Description      *string `json:"description" doc:"Description of the row in the session's locale, or null."`
	StateType        string  `json:"statetype" format:"uuid" doc:"Type of the state: created, enabled, disabled, deleted."`
	StateTypeCode    string  `json:"statetypecode" doc:"Code of the state type."`
	StateTypeName    *string `json:"statetypename" doc:"Name of the state type in the session's locale, or null."`
	State            string  `json:"state" format:"uuid" doc:"Current state of the workflow."`
	StateCode        string  `json:"statecode" doc:"Code of the current state."`
	StateLabel       *string `json:"statelabel" doc:"Label of the current state in the session's locale, or null."`
	LastUpdate       string  `json:"lastupdate" format:"date-time" doc:"Time of the last update; the ETag is made of it."`
	Owner            string  `json:"owner" format:"uuid" doc:"User that owns the object."`
	OwnerCode        string  `json:"ownercode" doc:"Login of the owner."`
	OwnerName        string  `json:"ownername" doc:"Name of the owner."`
	Created          string  `json:"created" format:"date-time" doc:"Time of creation."`
	Oper             string  `json:"oper" format:"uuid" doc:"User that made the last operation."`
	OperCode         string  `json:"opercode" doc:"Login of that user."`
	OperName         string  `json:"opername" doc:"Name of that user."`
	OperDate         string  `json:"operdate" format:"date-time" doc:"Operating date of the last operation."`
	Scope            string  `json:"scope" format:"uuid" doc:"Scope (the database's visibility boundary) the object lives in."`
	ScopeCode        string  `json:"scopecode" doc:"Code of the scope."`
	ScopeName        string  `json:"scopename" doc:"Name of the scope."`
	ScopeDescription *string `json:"scopedescription" doc:"Description of the scope, or null."`
}
