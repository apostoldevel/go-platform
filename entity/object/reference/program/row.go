package program

import "github.com/apostoldevel/go-platform/entity/object/reference"

// Row is a row of api.program: the columns of the reference family and the
// body of the program.
type Row struct {
	reference.Row
	Body string `json:"body" doc:"Text of the program (db.program.body, NOT NULL): SQL the task scheduler runs."`
}
