package resourcesmod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A manual measurement of the trim walk on a synthetic cache (t.TempDir(), never the real one):
//
//	PDX_MEASURE_TRIM=150000 go test ./internal/module/resources -run TestMeasureTrimWalk -v
//
// It lays out that many empty files over the 256 entry directories, walks once with nothing to delete (every file recent)
// and once with everything to delete, and prints the times. Skipped unless the variable is set.
func TestMeasureTrimWalk(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PDX_MEASURE_TRIM"))
	if n <= 0 {
		t.Skip("set PDX_MEASURE_TRIM=<number of files> to run")
	}
	dir := filepath.Join(t.TempDir(), "go-build")
	for i := 0; i < 256; i++ {
		if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("%02x", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	made := time.Now()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%02x", i%256), fmt.Sprintf("%016x-%c", i, "ad"[i%2]))
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("created %d files in %v", n, time.Since(made).Round(time.Millisecond))
	m := newTestModule(idleSampler(), nil)
	start := time.Now()
	if _, err := m.trimGoCache(context.Background(), dir, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Logf("walk with nothing to delete: %v (%.0f files/s)", time.Since(start).Round(time.Millisecond), float64(n)/time.Since(start).Seconds())
	start = time.Now()
	if _, err := m.trimGoCache(context.Background(), dir, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Logf("walk deleting everything: %v (%.0f files/s)", time.Since(start).Round(time.Millisecond), float64(n)/time.Since(start).Seconds())
}
