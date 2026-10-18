package facts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func removeFile(p string) error { return os.Remove(p) }

func TestPHPProbesEachBinaryOnceAndKeepsPerBinaryVersions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	php74 := exe(t, dir, "php74")
	php84 := exe(t, dir, "php84")
	run := &fakeRun{out: map[string]string{php74: "7.4.33", php84: "8.4.21"}}
	now := t0
	r := &ToolsReader{
		Run: run.run, Now: func() time.Time { return now }, TTL: time.Minute,
		PathDirs: []string{dir}, PHPDirs: []string{dir}, // the same directory in both lists
	}
	got := r.Get()
	if !reflect.DeepEqual(got.PHP, []string{"7.4.33", "8.4.21"}) {
		t.Fatalf("php %v", got.PHP)
	}
	probes := 0
	for _, c := range run.calls {
		if c == php74+" -r echo PHP_VERSION;" || c == php84+" -r echo PHP_VERSION;" {
			probes++
		}
	}
	if probes != 2 {
		t.Errorf("%d php probes for 2 binaries: %v", probes, run.calls)
	}

	// one binary fails to answer next time: its last version stays, the other updates
	now = now.Add(2 * time.Minute)
	run.fail = map[string]bool{php74: true}
	run.out[php84] = "8.4.22"
	got = r.Get()
	if !reflect.DeepEqual(got.PHP, []string{"7.4.33", "8.4.22"}) {
		t.Errorf("after a partial failure: %v", got.PHP)
	}

	// a binary that is gone takes its version with it
	now = now.Add(2 * time.Minute)
	if err := removeFile(php74); err != nil {
		t.Fatal(err)
	}
	got = r.Get()
	if !reflect.DeepEqual(got.PHP, []string{"8.4.22"}) {
		t.Errorf("after uninstall: %v", got.PHP)
	}
}
