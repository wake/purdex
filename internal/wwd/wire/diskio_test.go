package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// disk_io has the names the contract gives it (host-metrics spec §2.1), a null rate is written inside the entry, and a record with no
// disk_io does not carry the key.
func TestDiskIOFieldNames(t *testing.T) {
	r, w := 1500.5, 0.0
	b, err := json.Marshal(Facts{DiskIO: []DiskIO{{Device: "disk0", ReadBps: &r, WriteBps: &w}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `"disk_io":[{"device":"disk0","read_bps":1500.5,"write_bps":0,"read_iops":null,"write_iops":null}]`
	if !strings.Contains(string(b), want) {
		t.Errorf("want %s in %s", want, b)
	}
	b, _ = json.Marshal(Facts{})
	if strings.Contains(string(b), "disk_io") {
		t.Errorf("a record that read none carries the key: %s", b)
	}
	var f Facts
	if err := json.Unmarshal([]byte(`{"v":1,"disk_io":null}`), &f); err != nil || f.DiskIO != nil {
		t.Errorf("null reads as absent: %+v %v", f.DiskIO, err)
	}
}
