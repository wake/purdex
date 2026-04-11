package session

import (
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func TestBuildTerminalRelayArgs_Auto(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "auto")
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestBuildTerminalRelayArgs_TerminalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "terminal-first")
	assert.Equal(t, []string{"attach-session", "-t", "dev", "-f", "ignore-size"}, args)
}

func TestBuildTerminalRelayArgs_MinimalFirst(t *testing.T) {
	args := buildTerminalRelayArgs("dev", "minimal-first")
	// minimal-first does NOT add ignore-size — sizing is handled via OnStart callback
	assert.Equal(t, []string{"attach-session", "-t", "dev"}, args)
}

func TestWindowSizeForMode(t *testing.T) {
	assert.Equal(t, "latest", windowSizeForMode("auto"))
	assert.Equal(t, "smallest", windowSizeForMode("minimal-first"))
	assert.Equal(t, "latest", windowSizeForMode("terminal-first"))
	assert.Equal(t, "latest", windowSizeForMode(""))
}

// TestHandleTerminalWS_NoConfigRace is a race regression test for issue #26.
// HandleTerminalWS used to read m.core.Cfg.Terminal.SizingMode without holding
// CfgMu, while handlePutConfig writes that field under CfgMu.Lock. Running this
// test with `go test -race` must not report a data race.
func TestHandleTerminalWS_NoConfigRace(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	fake.AddSession("test-session", "/tmp") // auto-assigns $0

	mod := NewSessionModule(meta)
	c := core.New(core.CoreDeps{
		Config: &config.Config{
			Terminal: config.TerminalConfig{SizingMode: "auto"},
		},
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))

	code, err := EncodeSessionID("$0")
	require.NoError(t, err)

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	// Writer goroutine: continuously flips SizingMode under CfgMu.
	var writerWg sync.WaitGroup
	writerWg.Add(1)
	go func() {
		defer writerWg.Done()
		modes := []string{"auto", "terminal-first", "minimal-first"}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			c.CfgMu.Lock()
			c.Cfg.Terminal.SizingMode = modes[i%len(modes)]
			c.CfgMu.Unlock()
			runtime.Gosched()
		}
	}()

	// Cleanup must run even if the test body panics, to prevent the writer
	// goroutine from leaking into subsequent tests in the same package run.
	t.Cleanup(func() {
		closeStop()
		writerWg.Wait()
	})

	// Reader goroutines: concurrently invoke HandleTerminalWS. The WS upgrade
	// will fail because httptest.ResponseRecorder is not a Hijacker, but the
	// read of Cfg.Terminal.SizingMode (the field protected by the fix) runs
	// before the upgrade attempt. Each goroutine loops several times so total
	// reader activity is large enough to keep race-detector firing reliable
	// on busy CI hardware.
	var readerWg sync.WaitGroup
	for i := 0; i < 50; i++ {
		readerWg.Add(1)
		go func() {
			defer readerWg.Done()
			for j := 0; j < 20; j++ {
				req := httptest.NewRequest("GET", "/ws/terminal/"+code, nil)
				rec := httptest.NewRecorder()
				mod.HandleTerminalWS(rec, req, code)
			}
		}()
	}

	readerWg.Wait()
	closeStop()
	writerWg.Wait()
}
