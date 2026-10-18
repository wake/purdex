package wire

import (
	"strings"
	"testing"
)

// A worker names a job "source/id" (worker/ident), and an alias may hold a slash, so a job whose id holds one is refused where a job is
// decoded: (source "a", id "b/c") and (source "a/b", id "c") must not be one key.
func TestJobIDWithASlashIsRefused(t *testing.T) {
	src := replaceIn(t, "job_queued.json", `"manual": "", "manual_seq": 0`, `"manual": "", "manual_seq": 0`)
	var id string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, `"id":`) {
			id = line
			break
		}
	}
	if id == "" {
		t.Skip("no id line in the fixture")
	}
	if _, err := Decode[Job](src); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	bad := strings.Replace(string(src), id, strings.Replace(id, `"id": "`, `"id": "b/`, 1), 1)
	if bad == string(src) {
		t.Fatal("the id was not changed")
	}
	if _, err := Decode[Job]([]byte(bad)); err == nil || !strings.Contains(err.Error(), "slash") {
		t.Fatalf("an id with a slash: err %v", err)
	}
}
