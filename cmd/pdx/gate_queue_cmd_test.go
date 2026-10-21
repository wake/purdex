package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pdx gate status prints, and --json carries, who waits (#2941).
func TestGate_StatusListsTheWaiters(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	gofile := filepath.Join(t.TempDir(), "go")
	release := func() { _ = os.WriteFile(gofile, nil, 0o644) }
	defer release()
	hold := make(chan int, 1)
	var hout, herr bytes.Buffer
	go func() {
		hold <- slotGateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", "sh", "-c", "while [ ! -e " + gofile + " ]; do sleep 0.02; done"}, &hout, &herr)
	}()
	eventuallyOutput(t, func() bool {
		var o, e bytes.Buffer
		gateMain([]string{"status", "--config", cfg}, &o, &e)
		return strings.Contains(o.String(), "重 1/1 使用中")
	})
	wait := make(chan int, 1)
	var wout, werr bytes.Buffer
	go func() {
		wait <- slotGateMain([]string{"--config", cfg, "--class", "heavy", "--fg", "--", "sh", "-c", "exit 0"}, &wout, &werr)
	}()
	eventuallyOutput(t, func() bool {
		var o, e bytes.Buffer
		gateMain([]string{"status", "--config", cfg}, &o, &e)
		return strings.Contains(o.String(), "排隊 #1")
	})

	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Regexp(t, `排隊 #1  pid \d+  等 \d+s  sh -c exit 0  `, out.String())

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	var st struct {
		QueueHeavy []struct {
			Class string    `json:"class"`
			Place int       `json:"place"`
			PID   int       `json:"pid"`
			Since time.Time `json:"since"`
			Argv  []string  `json:"argv"`
			Cwd   string    `json:"cwd"`
		}
		QueueLight []any
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	require.Len(t, st.QueueHeavy, 1)
	assert.Equal(t, "heavy", st.QueueHeavy[0].Class)
	assert.Equal(t, 1, st.QueueHeavy[0].Place)
	assert.Equal(t, []string{"sh", "-c", "exit 0"}, st.QueueHeavy[0].Argv)
	assert.NotZero(t, st.QueueHeavy[0].PID)
	assert.False(t, st.QueueHeavy[0].Since.IsZero())
	assert.NotEmpty(t, st.QueueHeavy[0].Cwd)

	release()
	assert.Equal(t, 0, <-hold)
	assert.Equal(t, 0, <-wait)
}

// The flags may stand before or after the name / the setting (#2941).
func TestGate_FlagsMayFollowTheWords(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"set", "slots.heavy=2", "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, 0, gateMain([]string{"set", "--profile", "night", "slots.heavy=3", "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, 0, gateMain([]string{"profile", "use", "night", "--config", cfg}, &out, &errb), errb.String())
	assert.Equal(t, 0, gateMain([]string{"profile", "use", "--config", cfg, "night"}, &out, &errb), errb.String())
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"profile", "list", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "* night")
	assert.Equal(t, 2, gateMain([]string{"profile", "use", "a", "b", "--config", cfg}, &out, &errb), "two names are still a usage error")
	assert.Equal(t, 2, gateMain([]string{"profile", "use", "--config", cfg}, &out, &errb))
}

// release is built in: no table needed, and the command says so (#2941).
func TestGate_ReleaseIsBuiltIn(t *testing.T) {
	cfg, _ := gateEnv(t, noAdmission)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"profile", "use", "release", "--config", cfg}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "release 是內建的")
	assert.NotContains(t, out.String(), "還沒有 [gate.profiles.release]")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"profile", "list", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "* release")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"profile", "use", "ghost", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "還沒有 [gate.profiles.ghost]")
}
