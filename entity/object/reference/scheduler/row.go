package scheduler

import "github.com/apostoldevel/go-platform/entity/object/reference"

// Row is a row of api.scheduler: the columns of the reference family and the
// schedule. period is an interval the database writes as text; datestart and
// datestop are NOT NULL.
type Row struct {
	reference.Row
	Period    *string `json:"period" doc:"Period between runs as the database spells an interval (for example 00:15:00), or null."`
	DateStart string  `json:"datestart" format:"date-time" doc:"Time from which the scheduler fires."`
	DateStop  string  `json:"datestop" format:"date-time" doc:"Time after which the scheduler stops firing."`
}
