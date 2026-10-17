package assistmod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/gate"
)

// runScript runs <scripts>/<name> locally with ASSIST_HOST set (each script sends itself over ssh), in its own process group.
// At the timeout the whole group is ended. stdout is returned; stderr goes to the log.
func (m *Module) runScript(h gate.AssistHost, name string, args []string, timeout time.Duration) ([]byte, error) {
	if h.Scripts == "" {
		return nil, errors.New("no scripts directory set for this host")
	}
	ctx, cancel := context.WithTimeout(m.scriptCtx(), timeout)
	defer cancel()
	cmd := execstat.CommandContext(ctx, "assist.script", filepath.Join(h.Scripts, name), args...)
	cmd.Env = append(os.Environ(), "ASSIST_HOST="+h.SSH)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second // an orphan holding the pipes must not hold us
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // the script exited 0; a leftover of its group held the pipes and is ended just below
	}
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // whatever the script left running in its group goes with it
	}
	if s := strings.TrimSpace(stderr.String()); s != "" {
		m.logf("[assist] %s (%s): %s", name, h.SSH, s)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w%s", name, err, tail(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return ": " + s
}

// switchAnswer is what assist-on / assist-off print.
type switchAnswer struct {
	OK     bool   `json:"ok"`
	Switch string `json:"switch"`
	Detail string `json:"detail"`
}

// runSwitch runs the script of a switch kind and says whether the host did it, with a line for the caller.
func (m *Module) runSwitch(h gate.AssistHost, kind string) (string, error) {
	name, args, to := "assist-on", []string{"--json"}, m.timeouts.on
	if kind == "off" {
		name, args, to = "assist-off", []string{"--apply", "--json"}, m.timeouts.off
	}
	out, err := m.runScript(h, name, args, to)
	var a switchAnswer
	jerr := json.Unmarshal(bytes.TrimSpace(out), &a)
	if err != nil {
		// a script that says no (exit 1) prints its reason in the JSON detail
		if jerr == nil && a.Detail != "" {
			return a.Detail, errors.New(a.Detail)
		}
		return "", err
	}
	if jerr != nil {
		return "", fmt.Errorf("%s printed no JSON: %v", name, jerr)
	}
	if a.OK && a.Switch != kind { // the answer names the state it left the host in
		return "", fmt.Errorf("%s said ok but the host is %s", name, a.Switch)
	}
	if !a.OK {
		if a.Detail == "" {
			a.Detail = name + " said not ok"
		}
		return a.Detail, errors.New(a.Detail)
	}
	return a.Detail, nil
}
