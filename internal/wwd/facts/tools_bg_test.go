package facts

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundNeverBlocksTheCaller(t *testing.T) {
	root := t.TempDir()
	bin := exe(t, filepath.Join(root, "b"), "node")
	release := make(chan struct{})
	var runs atomic.Int32
	r := &ToolsReader{
		Run: func(ctx context.Context, tag, b string, args ...string) ([]byte, error) {
			runs.Add(1)
			<-release // a slow tool
			return []byte("v24.14.0"), nil
		},
		Now: func() time.Time { return t0 }, TTL: time.Minute, PathDirs: []string{filepath.Dir(bin)},
	}
	get := Background(r, func() time.Time { return t0 })
	done := make(chan struct{})
	go func() {
		got := get()
		if got.Node != nil || got.PHP == nil {
			t.Errorf("before the first round ends: %+v", got)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the caller waited for the tool")
	}
	get() // a second call while the refresh runs must not start another
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := get(); got.Node != nil {
			if *got.Node != "24.14.0" {
				t.Fatalf("node %v", *got.Node)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh never landed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := runs.Load(); n != 1 {
		t.Errorf("tool ran %d times, want 1", n)
	}
}
