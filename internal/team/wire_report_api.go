package team

// ---- T-1b2: the report routes' wire (plan "Shared contracts") ----

// CreateReportRequest is POST /api/team/reports: a ReportRequest plus the
// origin_inbox every team route carries. The JSON is flat.
type CreateReportRequest struct {
	OriginInbox string `json:"origin_inbox"`
	ReportRequest
}

// ReportLead is the lead a report is addressed to: its CURRENT ref and
// address, so the CLI can send the up message without a second call.
type ReportLead struct {
	Ref     string `json:"ref"`
	Address string `json:"address"`
}

// ReportResponse is the answer to POST /api/team/reports (201, or 200 on a
// replay): the stored report, the task as it is now (the member's view) and
// the lead.
type ReportResponse struct {
	Report Report     `json:"report"`
	Task   Task       `json:"task"`
	Lead   ReportLead `json:"lead"`
}

// ReportList is GET /api/team/reports, newest first; Reports is never null.
type ReportList struct {
	Reports []Report `json:"reports"`
}
