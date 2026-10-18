package facts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A platform with no process source reports procs and residents as null, not as
// an empty list that would say "read, and nothing runs".
func TestNoProcessSourceMeansNullNotEmpty(t *testing.T) {
	src := Sources{
		Now:   func() time.Time { return lt0 },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 1, Total: 10}, nil },
		Mem:   func() (RawMem, error) { return RawMem{TotalBytes: 1 << 30, PageSize: 4096}, nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
	}
	r, err := NewCollector(src).Sample()
	if err != nil {
		t.Fatal(err)
	}
	var f wire.Facts
	r.Apply(&f)
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"procs":null`) || !strings.Contains(string(b), `"residents":null`) {
		t.Errorf("procs and residents must be null: %s", b)
	}
}
