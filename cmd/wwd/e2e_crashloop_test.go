//go:build e2e

// The crash-loop rule's input, with the real binary (#3843): how a start records the end of the run before it, a start that could not bind
// that records nothing, and `wwd help` that starts nothing. The harness and its safety rules are in e2e_harness_test.go.
package main

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/store"
)

// prevExits are the prev_exit of every start record in the database of the host at home, oldest first.
func prevExits(t *testing.T, home string) []string {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "data", "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.DB().Query(`SELECT prev_exit FROM wwd_starts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// term asks the process to stop and waits for it to be gone.
func (p *proc) term() {
	p.t.Helper()
	p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.exited:
	case <-time.After(30 * time.Second):
		p.cmd.Process.Signal(syscall.SIGKILL)
		<-p.exited
		p.t.Fatal("wwd did not stop on SIGTERM")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A stop on request (SIGTERM) is a planned restart; a SIGKILL is a crash. The start after each says so.
func TestE2E_CrashLoopInputFollowsHowTheRunEnded(t *testing.T) {
	w := newWorld(t)
	o := procOpts{name: "crash"}
	port := freePort(t)
	p := w.start(o, port)
	mark := filepath.Join(p.home, "data", cleanExitFile)

	p.term()
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("a stop on request leaves the mark: %v", err)
	}
	p = w.start(o, port) // a planned restart
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("the next start consumes the mark: %v", err)
	}
	p.kill9()
	p = w.start(o, port) // after a crash
	p.term()
	p = w.start(o, port) // planned again
	if got, want := prevExits(t, p.home), []string{"first", "clean", "unclean", "clean"}; !equalStrings(got, want) {
		t.Fatalf("prev_exit %v, want %v", got, want)
	}
}

// A wwd that cannot bind its port is not a start of the service: it records nothing and leaves the mark of the run before it.
func TestE2E_AStartThatCannotBindRecordsNothingAndLeavesTheMark(t *testing.T) {
	w := newWorld(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	home := filepath.Join(w.root, "busy")
	mark := filepath.Join(home, "data", cleanExitFile)
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, []byte(strconv.FormatInt(time.Now().UnixMilli(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, ok := w.launch(procOpts{name: "busy"}, port)
	if ok {
		t.Fatalf("wwd started on a port that is taken:\n%s", p.logTail())
	}
	<-p.exited
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("a start that could not bind consumed the mark: %v", err)
	}
	if got := prevExits(t, p.home); len(got) != 0 {
		t.Fatalf("a start that could not bind was recorded: %v", got)
	}
}

// `wwd help` and a mistaken word are answered at once: nothing is opened or made.
func TestE2E_HelpAndAMistakeStartNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	for _, c := range []struct {
		args []string
		code int
	}{{[]string{"help"}, 0}, {[]string{"-h"}, 0}, {[]string{"--help"}, 0}, {[]string{"frobnicate"}, 2}, {[]string{"--bogus"}, 2}} {
		cmd := exec.Command(e2eBin, c.args...)
		cmd.Env = e2eEnv(home)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != c.code {
			t.Errorf("wwd %v: exit %d, want %d (stdout %q stderr %q)", c.args, code, c.code, out.String(), errb.String())
		}
		if c.code == 0 && !bytes.Contains(out.Bytes(), []byte("usage: wwd")) {
			t.Errorf("wwd %v: no usage on stdout: %q", c.args, out.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("something was made under the home: %v", err)
	}
}
