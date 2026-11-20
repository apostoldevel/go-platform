package message

// Row is a row of api.message in the names the database gives its columns
// (lower case): the object, the document, the agent that delivers it, the
// message's own columns and the document's priority and area. It is what GET
// /messages and GET /messages/{id} answer, and the schema of the OpenAPI
// document.
//
// A column is a pointer where the view can give NULL: the texts that come from
// a LEFT JOIN on the session's locale, the object's parent, the message's
// subject and content (nullable in db.message) and the descriptions of the
// scope and the area.
type Row struct {
	ID                   string  `json:"id" format:"uuid" doc:"Identifier of the message (the same as its document and object)."`
	Object               string  `json:"object" format:"uuid" doc:"Identifier of the underlying object."`
	Parent               *string `json:"parent" format:"uuid" doc:"Parent object, or null."`
	Entity               string  `json:"entity" format:"uuid" doc:"Entity of the object."`
	EntityCode           string  `json:"entitycode" doc:"Code of the entity."`
	EntityName           *string `json:"entityname" doc:"Name of the entity in the session's locale, or null where it has no text."`
	Class                string  `json:"class" format:"uuid" doc:"Class of the object."`
	ClassCode            string  `json:"classcode" doc:"Code of the class."`
	ClassLabel           *string `json:"classlabel" doc:"Label of the class in the session's locale, or null."`
	Type                 string  `json:"type" format:"uuid" doc:"Type of the message."`
	TypeCode             string  `json:"typecode" doc:"Code of the type, for example message.outbox."`
	TypeName             *string `json:"typename" doc:"Name of the type in the session's locale, or null."`
	TypeDescription      *string `json:"typedescription" doc:"Description of the type in the session's locale, or null."`
	StateType            string  `json:"statetype" format:"uuid" doc:"Type of the state: created, enabled, disabled, deleted."`
	StateTypeCode        string  `json:"statetypecode" doc:"Code of the state type."`
	StateTypeName        *string `json:"statetypename" doc:"Name of the state type in the session's locale, or null."`
	State                string  `json:"state" format:"uuid" doc:"Current state of the workflow."`
	StateCode            string  `json:"statecode" doc:"Code of the current state."`
	StateLabel           *string `json:"statelabel" doc:"Label of the current state in the session's locale, or null."`
	Created              string  `json:"created" format:"date-time" doc:"Time of creation."`
	LastUpdate           string  `json:"lastupdate" format:"date-time" doc:"Time of the last update; the ETag is made of it."`
	OperDate             string  `json:"operdate" format:"date-time" doc:"Operating date of the last operation."`
	Code                 string  `json:"code" doc:"Code of the message: its business key."`
	Name                 *string `json:"name" doc:"The view gives the object's label under this name as well; null where it has no text."`
	Label                *string `json:"label" doc:"Label of the object in the session's locale, or null."`
	Description          *string `json:"description" doc:"Description of the document in the session's locale, or null."`
	AgentType            string  `json:"agenttype" format:"uuid" doc:"Type of the agent."`
	AgentTypeCode        string  `json:"agenttypecode" doc:"Code of the agent's type."`
	AgentTypeName        *string `json:"agenttypename" doc:"Name of the agent's type in the session's locale, or null."`
	AgentTypeDescription *string `json:"agenttypedescription" doc:"Description of the agent's type in the session's locale, or null."`
	Agent                string  `json:"agent" format:"uuid" doc:"Agent that delivers the message (api.agent)."`
	AgentCode            string  `json:"agentcode" doc:"Code of the agent."`
	AgentName            *string `json:"agentname" doc:"Name of the agent in the session's locale, or null."`
	AgentDescription     *string `json:"agentdescription" doc:"Description of the agent in the session's locale, or null."`
	Profile              string  `json:"profile" doc:"Profile of the message on the agent (db.message.profile, NOT NULL)."`
	Address              string  `json:"address" doc:"Address of the message on the agent (db.message.address, NOT NULL)."`
	Subject              *string `json:"subject" doc:"Subject, or null."`
	Content              *string `json:"content" doc:"Content, or null."`
	Priority             string  `json:"priority" format:"uuid" doc:"Priority of the document."`
	PriorityCode         string  `json:"prioritycode" doc:"Code of the priority."`
	PriorityName         *string `json:"priorityname" doc:"Name of the priority in the session's locale, or null."`
	PriorityDescription  *string `json:"prioritydescription" doc:"Description of the priority in the session's locale, or null."`
	Owner                string  `json:"owner" format:"uuid" doc:"User that owns the object."`
	OwnerCode            string  `json:"ownercode" doc:"Login of the owner."`
	OwnerName            string  `json:"ownername" doc:"Name of the owner."`
	Oper                 string  `json:"oper" format:"uuid" doc:"User that made the last operation."`
	OperCode             string  `json:"opercode" doc:"Login of that user."`
	OperName             string  `json:"opername" doc:"Name of that user."`
	Area                 string  `json:"area" format:"uuid" doc:"Area of the document."`
	AreaCode             string  `json:"areacode" doc:"Code of the area."`
	AreaName             string  `json:"areaname" doc:"Name of the area."`
	AreaDescription      *string `json:"areadescription" doc:"Description of the area, or null."`
	Scope                string  `json:"scope" format:"uuid" doc:"Scope (the database's visibility boundary) the object lives in."`
	ScopeCode            string  `json:"scopecode" doc:"Code of the scope."`
	ScopeName            string  `json:"scopename" doc:"Name of the scope."`
	ScopeDescription     *string `json:"scopedescription" doc:"Description of the scope, or null."`
}
