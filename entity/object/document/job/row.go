package job

// Row is a row of api.job in the names the database gives its columns (lower
// case). It is what GET list and GET by id answer, and the schema of the
// OpenAPI document. A column is a pointer where the view can give NULL: the
// texts of a LEFT JOIN on the session's locale, the columns the table leaves
// nullable and the ones the view masks.
type Row struct {
	ID                  string  `json:"id" format:"uuid" doc:"Identifier of the job (the same as its object)."`
	Object              string  `json:"object" format:"uuid" doc:"Identifier of the underlying object."`
	Parent              *string `json:"parent" format:"uuid" doc:"Parent object, or null."`
	Entity              string  `json:"entity" format:"uuid" doc:"Entity of the object."`
	EntityCode          string  `json:"entitycode" doc:"Code of the entity."`
	EntityName          *string `json:"entityname" doc:"Name of the entity in the session's locale, or null where it has no text."`
	Class               string  `json:"class" format:"uuid" doc:"Class of the object."`
	ClassCode           string  `json:"classcode" doc:"Code of the class."`
	ClassLabel          *string `json:"classlabel" doc:"Label of the class in the session's locale, or null."`
	Type                string  `json:"type" format:"uuid" doc:"Type of the job."`
	TypeCode            string  `json:"typecode" doc:"Code of the type."`
	TypeName            *string `json:"typename" doc:"Name of the type in the session's locale, or null."`
	TypeDescription     *string `json:"typedescription" doc:"Description of the type in the session's locale, or null."`
	StateType           string  `json:"statetype" format:"uuid" doc:"Type of the state: created, enabled, disabled, deleted."`
	StateTypeCode       string  `json:"statetypecode" doc:"Code of the state type."`
	StateTypeName       *string `json:"statetypename" doc:"Name of the state type in the session's locale, or null."`
	State               string  `json:"state" format:"uuid" doc:"Current state of the workflow."`
	StateCode           string  `json:"statecode" doc:"Code of the current state."`
	StateLabel          *string `json:"statelabel" doc:"Label of the current state in the session's locale, or null."`
	Created             string  `json:"created" format:"date-time" doc:"Time of creation."`
	LastUpdate          string  `json:"lastupdate" format:"date-time" doc:"Time of the last update; the ETag is made of it."`
	OperDate            string  `json:"operdate" format:"date-time" doc:"Operating date of the last operation."`
	Code                string  `json:"code" doc:"Unique job code within the scope."`
	Name                *string `json:"name" doc:"Name of the job in the session's locale, or null where it has no text."`
	Label               *string `json:"label" doc:"Label of the object in the session's locale, or null."`
	Description         *string `json:"description" doc:"Description of the job in the session's locale, or null."`
	Scheduler           string  `json:"scheduler" format:"uuid" doc:"Scheduler that owns this job (defines recurrence period)."`
	SchedulerCode       string  `json:"schedulercode" doc:"Code of the scheduler."`
	SchedulerName       *string `json:"schedulername" doc:"Name of the scheduler in the session's locale, or null."`
	Datestart           string  `json:"datestart" format:"date-time" doc:"Start of the scheduler's validity period."`
	Datestop            string  `json:"datestop" format:"date-time" doc:"End of the scheduler's validity period."`
	Period              *string `json:"period" doc:"Recurrence period of the scheduler, as the database prints an interval, or null."`
	DateRun             string  `json:"daterun" format:"date-time" doc:"Next scheduled execution timestamp."`
	Program             string  `json:"program" format:"uuid" doc:"Program to execute when the job runs."`
	ProgramCode         string  `json:"programcode" doc:"Code of the program."`
	ProgramName         *string `json:"programname" doc:"Name of the program in the session's locale, or null."`
	Priority            string  `json:"priority" format:"uuid" doc:"Priority of the document."`
	PriorityCode        string  `json:"prioritycode" doc:"Code of the priority."`
	PriorityName        *string `json:"priorityname" doc:"Name of the priority in the session's locale, or null."`
	PriorityDescription *string `json:"prioritydescription" doc:"Description of the priority in the session's locale, or null."`
	Owner               string  `json:"owner" format:"uuid" doc:"User that owns the object."`
	OwnerCode           string  `json:"ownercode" doc:"Login of the owner."`
	OwnerName           string  `json:"ownername" doc:"Name of the owner."`
	Oper                string  `json:"oper" format:"uuid" doc:"User that made the last operation."`
	OperCode            string  `json:"opercode" doc:"Login of that user."`
	OperName            string  `json:"opername" doc:"Name of that user."`
	Area                string  `json:"area" format:"uuid" doc:"Area of the document."`
	AreaCode            string  `json:"areacode" doc:"Code of the area."`
	AreaName            string  `json:"areaname" doc:"Name of the area."`
	AreaDescription     *string `json:"areadescription" doc:"Description of the area, or null."`
	Scope               string  `json:"scope" format:"uuid" doc:"Scope (the database's visibility boundary) the object lives in."`
	ScopeCode           string  `json:"scopecode" doc:"Code of the scope."`
	ScopeName           string  `json:"scopename" doc:"Name of the scope."`
	ScopeDescription    *string `json:"scopedescription" doc:"Description of the scope, or null."`
}
