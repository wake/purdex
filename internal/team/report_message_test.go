package team

import (
	"strings"
	"testing"
)

// The up message is spelled out here, not built from the constants.
func TestReportUpMessage(t *testing.T) {
	for name, c := range map[string]struct {
		r    Report
		want string
	}{
		"ack": {Report{Kind: ReportAck, Task: "8f2c0f-3", Summary: "starting"}, "[report ack 8f2c0f-3] starting"},
		"question": {Report{Kind: ReportQuestion, Task: "8f2c0f-3", Summary: "which db?", Needs: "user"},
			"[report question 8f2c0f-3] which db?\nneeds: user"},
		"ready": {Report{Kind: ReportReady, Task: "8f2c0f-3", Summary: "PR up", PR: 123, Reviews: []string{"R1=job-a", "R2=job-b"}, Body: "findings...\n\n"},
			"[report ready 8f2c0f-3] PR up\npr: #123\nreviews: R1=job-a R2=job-b\n\nfindings..."},
		"merged": {Report{Kind: ReportMerged, Task: "8f2c0f-3", Summary: "in", PR: 123, SHA: "abcdef1"},
			"[report merged 8f2c0f-3] in\npr: #123\nsha: abcdef1"},
		"blank body": {Report{Kind: ReportDone, Task: "8f2c0f-3", Summary: "ok", Body: " \n"}, "[report done 8f2c0f-3] ok"},
	} {
		if got := ReportUpMessage(c.r); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, c.want)
		}
	}
	if m := ReportUpMessage(Report{Kind: ReportAck, Task: "x", Summary: "s"}); strings.HasPrefix(m, "[pdx team]") || strings.HasPrefix(m, "[pdx-relay") {
		t.Errorf("collides with a notice prefix: %q", m)
	}
}
