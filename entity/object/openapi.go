package object

import (
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/openapi"
)

// Row is a row of the view api.object, its columns in order: what every object
// has whatever its entity. Every column but the id is a pointer (nullable): a
// view does not say which can be NULL.
type Row struct {
	ID               string  `json:"id" format:"uuid" doc:"Identifier of the object."`
	Parent           *string `json:"parent" format:"uuid" doc:"Parent object, or null."`
	Entity           *string `json:"entity" format:"uuid" doc:"Entity of the object."`
	Entitycode       *string `json:"entitycode" doc:"Code of the entity."`
	Entityname       *string `json:"entityname" doc:"Name of the entity in the session's locale."`
	Class            *string `json:"class" format:"uuid" doc:"Class of the object."`
	Classcode        *string `json:"classcode" doc:"Code of the class."`
	Classlabel       *string `json:"classlabel" doc:"Label of the class in the session's locale."`
	Type             *string `json:"type" format:"uuid" doc:"Type of the object."`
	Typecode         *string `json:"typecode" doc:"Code of the type."`
	Typename         *string `json:"typename" doc:"Name of the type in the session's locale."`
	Typedescription  *string `json:"typedescription" doc:"Description of the type in the session's locale."`
	Label            *string `json:"label" doc:"Label of the object in the session's locale."`
	Text             *string `json:"text" doc:"Text of the object in the session's locale."`
	Statetype        *string `json:"statetype" format:"uuid" doc:"Type of the current state."`
	Statetypecode    *string `json:"statetypecode" doc:"Code of the type of the current state."`
	Statetypename    *string `json:"statetypename" doc:"Name of the type of the current state."`
	State            *string `json:"state" format:"uuid" doc:"Current state."`
	Statecode        *string `json:"statecode" doc:"Code of the current state."`
	Statelabel       *string `json:"statelabel" doc:"Label of the current state."`
	Lastupdate       *string `json:"lastupdate" format:"date-time" doc:"Time of the last modification; the ETag of the row is made of it."`
	Owner            *string `json:"owner" format:"uuid" doc:"Owner of the object."`
	Ownercode        *string `json:"ownercode" doc:"Login of the owner."`
	Ownername        *string `json:"ownername" doc:"Name of the owner."`
	Created          *string `json:"created" format:"date-time" doc:"Time the object was created."`
	Oper             *string `json:"oper" format:"uuid" doc:"User who performed the last operation."`
	Opercode         *string `json:"opercode" doc:"Login of that user."`
	Opername         *string `json:"opername" doc:"Name of that user."`
	Operdate         *string `json:"operdate" format:"date-time" doc:"Operating date of the last operation."`
	Scope            *string `json:"scope" format:"uuid" doc:"Scope (tenant boundary) of the object."`
	Scopecode        *string `json:"scopecode" doc:"Code of the scope."`
	Scopename        *string `json:"scopename" doc:"Name of the scope."`
	Scopedescription *string `json:"scopedescription" doc:"Description of the scope."`
}

// AccessEntry is a row of the view api.object_access: one grant on an object.
type AccessEntry struct {
	Object      *string `json:"object" format:"uuid" doc:"The object."`
	UserID      *string `json:"userid" format:"uuid" doc:"User or group the entry is for."`
	Deny        *int32  `json:"deny" doc:"Denied bits."`
	Allow       *int32  `json:"allow" doc:"Allowed bits."`
	Mask        *int32  `json:"mask" doc:"The effective mask of the entry."`
	Type        *string `json:"type" doc:"Kind of the subject: U for a user, G for a group."`
	Username    *string `json:"username" doc:"Login of the user or group."`
	Name        *string `json:"name" doc:"Name of the user or group."`
	Description *string `json:"description" doc:"Description of the user or group."`
}

// AccessBits is the answer of api.decode_object_access: what a user may do
// with an object.
type AccessBits struct {
	S *bool `json:"s" doc:"May select (read) the object."`
	U *bool `json:"u" doc:"May update the object."`
	D *bool `json:"d" doc:"May delete the object."`
}

// FileRow is a row of the view api.object_file: a file attached to an object,
// without its bytes.
type FileRow struct {
	Object    *string `json:"object" format:"uuid" doc:"The object the file is attached to."`
	File      *string `json:"file" format:"uuid" doc:"Identifier of the file."`
	Label     *string `json:"label" doc:"Label of the object in the session's locale."`
	Owner     *string `json:"owner" format:"uuid" doc:"Owner of the file."`
	Ownercode *string `json:"ownercode" doc:"Login of the owner."`
	Ownername *string `json:"ownername" doc:"Name of the owner."`
	Name      *string `json:"name" doc:"File name."`
	Path      *string `json:"path" doc:"Path of the file."`
	Size      *int32  `json:"size" doc:"Size in bytes."`
	Date      *string `json:"date" format:"date-time" doc:"Date of the file."`
	Link      *string `json:"link" doc:"URL of the file where it is stored elsewhere."`
	Hash      *string `json:"hash" doc:"Hash of the content."`
	Text      *string `json:"text" doc:"Text of the file."`
	Type      *string `json:"type" doc:"Media type."`
	Loaded    *string `json:"loaded" format:"date-time" doc:"Time the file was attached to the object."`
	Picture   *string `json:"picture" doc:"Picture of the owner."`
}

// FileDataRow is a row of the view api.object_file_data: a file with its bytes.
type FileDataRow struct {
	FileRow
	Data *string `json:"data" doc:"Content of the file, base64."`
}

// Operations is the module's description of its routes.
func (m *module) Operations() []openapi.Operation { return openapi.Docs(m.docs()...) }

var _ openapi.Documented = (*module)(nil)

const (
	idDesc = "Identifier of the object."
	notVis = "There is no such object, or the caller may not see it; the two are not told apart."
)

func (m *module) docs() []openapi.Doc {
	p := objects.Prefix
	row := openapi.Schema{Name: "Object", Value: Row{}, Mode: openapi.Row,
		Description: "A row of api.object: what every object has whatever its entity. Every property is present, a property that has no value is null. In a list answered with `fields`, only the named properties are present."}
	access := openapi.Schema{Name: "ObjectAccess", Value: AccessEntry{}, Mode: openapi.Row,
		Description: "A row of api.object_access: one grant on an object."}
	bits := openapi.Schema{Name: "ObjectAccessBits", Value: AccessBits{}, Mode: openapi.Row,
		Description: "What one user may do with an object, as api.decode_object_access gives it."}
	file := openapi.Schema{Name: "ObjectFile", Value: FileRow{}, Mode: openapi.Row,
		Description: "A row of api.object_file: a file attached to an object, without its bytes."}
	fileData := openapi.Schema{Name: "ObjectFileData", Value: FileDataRow{}, Mode: openapi.Row,
		Description: "A row of api.object_file_data: a file attached to an object, with its bytes in data."}
	fileIn := openapi.Schema{Name: "ObjectFileInput", Value: fileBody{}, Mode: openapi.Create,
		Description: "A file to attach to an object. A key the schema does not name is refused with 400, and so is a negative size."}
	method := openapi.Schema{Name: "ObjectMethod", Value: entity.MethodRow{}, Mode: openapi.Row,
		Description: "A method of an object's class, as GET <xs>/{id}/methods gives it."}
	etag := openapi.Header{Name: "ETag", Description: "Weak ETag of the row: its last update, or a digest of the row.", Schema: openapi.String}
	id := openapi.PathID("id", idDesc)
	badID := "The id is not a UUID."
	runBody := &openapi.Body{Description: "Parameters passed to the action or the method. Optional; a JSON object, of which at most 1 MiB is read.",
		Schema: map[string]any{"type": "object", "additionalProperties": true}}

	return []openapi.Doc{
		{Method: "GET", Path: p, ID: "objects_list", Tag: "objects",
			Summary:     "List objects of every entity",
			Description: "The rows of api.object the caller may see, whatever their entity: filtered, sorted and paged. total counts the rows of the same selection before paging. api.list_object and api.count_object.",
			Params:      openapi.ListParams(),
			OK:          openapi.OK200("The page.", openapi.Page(row)),
			Bad:         "A bad filter, sort, fields or page value, or a query parameter the list does not know."},
		{Method: "GET", Path: p + "/{id}", ID: "objects_get", Tag: "objects",
			Summary:     "Get an object",
			Description: "The generic row of an object, api.get_object. An entity's own prefix answers the same object with the columns of its entity.",
			Params: []openapi.Param{id, {Name: "If-None-Match", In: "header", Schema: openapi.String,
				Description: "The ETag of the copy the client holds: when it is current the answer is 304 with no body."}},
			OK:       []openapi.Response{{Status: "200", Description: "The row.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      badID,
			NotFound: notVis,
			Extra: []openapi.Response{{Status: "304", Description: "The row has not changed since If-None-Match. No body.",
				Headers: []openapi.Header{etag}}}},
		{Method: "GET", Path: p + "/{id}/methods", ID: "objects_methods", Tag: "objects",
			Summary:     "Methods of an object now",
			Description: "The methods of the object's class bound to its current state, with the caller's rights to each, in the workflow's order (api.get_object_methods). Not paged; query parameters are not read.",
			Params:      []openapi.Param{id},
			OK:          openapi.OK200("The methods.", openapi.Array(method)),
			Bad:         badID,
			NotFound:    notVis},
		{Method: "POST", Path: p + "/{id}/actions/{action}", ID: "objects_action", Tag: "objects",
			Summary:     "Execute an action of an object",
			Description: "Runs the object's method for an action given by its code (api.execute_object_action), in one transaction, on an object the caller may see. The answer is the object's row after the action. An action the class does not have, or one the state does not allow, is refused by the database.",
			Params: []openapi.Param{id, {Name: "action", In: "path", Required: true,
				Schema:      map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_]{0,63}$"},
				Description: "Code of the action. Anything but a lower-case identifier is refused with 400."}},
			Body:     runBody,
			OK:       []openapi.Response{{Status: "200", Description: "The object after the action.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:      "The id is not a UUID, the action code is malformed, or the body is not empty and not a JSON object.",
			NotFound: notVis},
		{Method: "POST", Path: p + "/{id}/methods/{method}", ID: "objects_run_method", Tag: "objects",
			Summary:     "Execute a method of an object",
			Description: "Runs a method of the object's class by its identifier (api.execute_method), in one transaction, on an object the caller may see; the identifiers are those of GET …/methods. The answer is the object's row after the run.",
			Params:      []openapi.Param{id, openapi.PathID("method", "Identifier of the method.")},
			Body:        runBody,
			OK:          []openapi.Response{{Status: "200", Description: "The object after the method.", Schema: row, Headers: []openapi.Header{etag}}},
			Bad:         "The id or the method is not a UUID, or the body is not empty and not a JSON object.",
			NotFound:    notVis},
		{Method: "GET", Path: p + "/{id}/access", ID: "objects_access", Tag: "objects",
			Summary:     "Who holds what on an object",
			Description: "The access entries of the object (api.object_access): the users and groups with their allowed and denied bits. The database lists them to an administrator and to a caller who holds the update right on the object; to anyone else the list is empty.",
			Params:      []openapi.Param{id},
			OK:          openapi.OK200("The entries.", openapi.Array(access)),
			Bad:         badID,
			NotFound:    notVis},
		{Method: "PUT", Path: p + "/{id}/access", ID: "objects_access_set", Tag: "objects",
			Summary:     "Grant access to an object",
			Description: "Sets one grant of a user or a group on the object (api.chmodo). The function is open to administrators only: any other session gets 403 (ERR-403-013) once the body has passed validation. The mask is six bits, select, update and delete for allow and for deny, so that 7 allows all three; it is refused with 400 above 63, before the database. The answer is the access entries after the grant (as GET …/access).",
			Params:      []openapi.Param{id},
			Body: &openapi.Body{Required: true, Schema: openapi.Obj("The grant. `recursive` and `objectset` belong to a class and are refused here with 400; any other key is refused too.", map[string]any{
				"userid": map[string]any{"type": "string", "format": "uuid", "description": "User or group to grant to."},
				"mask":   map[string]any{"type": "integer", "minimum": 0, "maximum": 63, "description": "The six-bit mask."},
			})},
			OK:       openapi.OK200("The access entries after the grant.", openapi.Array(access)),
			Bad:      "The id or userid is not a UUID, the mask is missing, negative or above 63, an unknown key or recursive/objectset is given, or the body is not JSON.",
			NotFound: notVis},
		{Method: "GET", Path: p + "/{id}/access/decode", ID: "objects_access_decode", Tag: "objects",
			Summary:     "The rights of a user on an object",
			Description: "The select, update and delete bits of one user on the object, decoded (api.decode_object_access). Without userid, the caller's own. The function is open to administrators only: any other session gets 403 (ERR-403-013).",
			Params:      []openapi.Param{id, openapi.QID("userid", false, "The user to decode for; the caller when absent. A UUID, anything else is refused with 400.")},
			OK:          openapi.OK200("The bits.", bits),
			Bad:         "The id or userid is not a UUID.",
			NotFound:    notVis},
		{Method: "GET", Path: p + "/{id}/files", ID: "objects_files_list", Tag: "objects",
			Summary:     "List the files of an object",
			Description: "The files attached to the object, without their bytes, filtered, sorted and paged (api.list_object_file and api.count_object_file over `object = id`). A single file with its bytes is GET …/files/{file}.",
			Params:      append([]openapi.Param{id}, openapi.ListParams()...),
			OK:          openapi.OK200("The page.", openapi.Page(file)),
			Bad:         "The id is not a UUID, or a bad filter, sort, fields or page value.",
			NotFound:    notVis},
		{Method: "POST", Path: p + "/{id}/files", ID: "objects_files_add", Tag: "objects",
			Summary:     "Attach files to an object",
			Description: "Attaches the files of the body to the object (api.set_object_files_json); a file with the name and path of one already attached replaces it, so the call is an upsert. The body is a JSON array, not a multipart form: the bytes of a file go base64-encoded in `data`; at most 64 MiB of the body is read. The files already attached stay; DELETE …/files clears them first. The answer is the rows written, as the list gives them.",
			Params:      []openapi.Param{id},
			Body: &openapi.Body{Required: true, Description: "A non-empty array of files.",
				Schema: map[string]any{"type": "array", "minItems": 1, "items": fileIn}},
			OK:       openapi.OK200("The files written.", openapi.Array(file)),
			Bad:      "The id is not a UUID, the body is not a non-empty JSON array of files, a file lacks a name or has a negative size or an unknown key, or the database refuses a value (data that is not base64).",
			NotFound: notVis},
		{Method: "DELETE", Path: p + "/{id}/files", ID: "objects_files_clear", Tag: "objects",
			Summary:     "Detach every file of an object",
			Description: "Removes every file attached to the object (api.clear_object_files).",
			Params:      []openapi.Param{id},
			OK:          []openapi.Response{{Status: "204", Description: "The files are gone. No body."}},
			Bad:         badID,
			NotFound:    notVis},
		{Method: "GET", Path: p + "/{id}/files/{file}", ID: "objects_files_get", Tag: "objects",
			Summary:     "Get a file of an object",
			Description: "One file attached to the object with its bytes in `data`, base64 (api.get_object_file).",
			Params:      []openapi.Param{id, openapi.PathID("file", "Identifier of the file.")},
			OK:          openapi.OK200("The file.", fileData),
			Bad:         "The id or the file is not a UUID.",
			NotFound:    "There is no such object, or the caller may not see it, or the object has no such file."},
		{Method: "DELETE", Path: p + "/{id}/files/{file}", ID: "objects_files_delete", Tag: "objects",
			Summary:     "Detach a file from an object",
			Description: "Removes one file from the object (api.delete_object_file).",
			Params:      []openapi.Param{id, openapi.PathID("file", "Identifier of the file.")},
			OK:          []openapi.Response{{Status: "204", Description: "The file is gone. No body."}},
			Bad:         "The id or the file is not a UUID.",
			NotFound:    "There is no such object, or the caller may not see it, or the object has no such file."},
		{Method: "GET", Path: search, ID: "search_find", Tag: "search",
			Summary:     "Search objects",
			Description: "Full-text search over the objects the caller may see (api.search), in the locale of the session unless `locale` names another. Not paged.",
			Params: []openapi.Param{
				openapi.Q("q", openapi.String, true, "The text to look for. Required; blank is refused with 400."),
				openapi.Q("entities", openapi.String, false, "Comma-separated entity codes to search in (lower-case identifiers; anything else is refused with 400); all entities when absent."),
				openapi.Q("locale", openapi.String, false, "Code of the locale to search in; the session's own when absent. The database searches in Russian for ru and in English for any other code."),
			},
			OK:  openapi.OK200("The matching objects.", openapi.Array(row)),
			Bad: "q is absent or blank, or entities holds something that is not an entity code."},
	}
}
