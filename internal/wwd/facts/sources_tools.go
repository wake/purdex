package facts

import (
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// systemTools is the toolchain reader for this host, nil when the home
// directory is unknown.
// extra is the resolved [tools] extra_path.
func systemTools(extra []string) func() wire.Tools {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return Background(NewToolsReader(home, os.Getenv("PATH"), extra), time.Now)
}

// Background wraps a ToolsReader so the sampling loop never waits for it: asking
// every tool forks (about 10 s for the first round on a Mac with several PHP
// versions), so a stale answer starts one refresh in a goroutine and the caller
// gets what is known now (all null before the first round ends).
func Background(r *ToolsReader, now func() time.Time) func() wire.Tools {
	var (
		mu    sync.Mutex
		cur   = wire.Tools{PHP: []string{}}
		busy  bool
		asked time.Time
	)
	return func() wire.Tools {
		mu.Lock()
		defer mu.Unlock()
		if t := now(); !busy && (asked.IsZero() || t.Sub(asked) >= r.TTL) {
			busy, asked = true, t
			go func() {
				v := r.Get()
				mu.Lock()
				cur, busy = v, false
				mu.Unlock()
			}()
		}
		return cloneTools(cur)
	}
}
