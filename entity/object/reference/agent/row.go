package agent

import "github.com/apostoldevel/go-platform/entity/object/reference"

// Row is a row of api.agent: the columns of the reference family and the
// vendor of the agent, in the names the database gives them. The vendor
// is a NOT NULL reference; its code, name and description come from a LEFT
// JOIN, so they are pointers.
type Row struct {
	reference.Row
	Vendor            string  `json:"vendor" format:"uuid" doc:"Vendor of the agent (api.vendor)."`
	VendorCode        *string `json:"vendorcode" doc:"Code of the vendor, or null."`
	VendorName        *string `json:"vendorname" doc:"Name of the vendor in the session's locale, or null."`
	VendorDescription *string `json:"vendordescription" doc:"Description of the vendor in the session's locale, or null."`
}
