package team

import (
	"encoding/json"
	"testing"
)

// The request is flat: the ReportRequest fields sit beside origin_inbox, so
// the CLI and the daemon share one body.
func TestReportAPI_RequestIsFlatSnakeCase(t *testing.T) {
	b, err := json.Marshal(CreateReportRequest{OriginInbox: "i", ReportRequest: ReportRequest{ID: "r", Task: "a-1", Kind: ReportReady,
		Summary: "s", PR: 3, Reviews: []string{"R1=j"}}})
	want := `{"origin_inbox":"i","id":"r","task":"a-1","kind":"ready","summary":"s","pr":3,"reviews":["R1=j"]}`
	if err != nil || string(b) != want {
		t.Fatalf("= %s (err %v), want %s", b, err, want)
	}
	var back CreateReportRequest
	if err := json.Unmarshal([]byte(want), &back); err != nil || back.OriginInbox != "i" || back.Kind != ReportReady || back.PR != 3 {
		t.Fatalf("decoded %+v (err %v)", back, err)
	}
}

func TestReportAPI_ResponseShapeAndNoNullList(t *testing.T) {
	b, _ := json.Marshal(ReportList{Reports: []Report{}})
	if string(b) != `{"reports":[]}` {
		t.Errorf("ReportList = %s", b)
	}
	b, _ = json.Marshal(ReportLead{Ref: "_abc123", Address: "mlab/_abc123"})
	if string(b) != `{"ref":"_abc123","address":"mlab/_abc123"}` {
		t.Errorf("ReportLead = %s", b)
	}
	var keys map[string]json.RawMessage
	b, _ = json.Marshal(ReportResponse{})
	if err := json.Unmarshal(b, &keys); err != nil || len(keys) != 3 || keys["report"] == nil || keys["task"] == nil || keys["lead"] == nil {
		t.Errorf("ReportResponse keys = %v (err %v)", keys, err)
	}
}
