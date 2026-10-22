package dev

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/core"
)

// installedPdx is a running-binary stand-in outside the repo, holding content "old".
func installedPdx(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "pdx")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	return target
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// The new file is run before it replaces the only installed binary: one that cannot start must leave everything as it was.
func TestHandleDaemonRebuild_FailedVerificationChangesNothing(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := installedPdx(t)
	if err := os.WriteFile(target+".prev", []byte("older"), 0755); err != nil {
		t.Fatal(err)
	}

	n := 0
	m := &DevModule{
		repoRoot:   dir,
		selfPathFn: func() (string, error) { return target, nil },
		verifyFn:   func(context.Context, string, string) error { return errors.New("exec format error") },
		restartFn:  func(string, string, func()) error { n++; return nil },
	}
	body := postRebuild(t, m)

	if got := readString(t, target); got != "old" {
		t.Errorf("target changed to %q", got)
	}
	if got := readString(t, target+".prev"); got != "older" {
		t.Errorf(".prev changed to %q", got)
	}
	if exists(target + ".new") {
		t.Error(".new left behind")
	}
	if n != 0 {
		t.Errorf("restart called %d times", n)
	}
	if !strings.Contains(body, `"type":"error"`) || !strings.Contains(body, "verification") || !strings.Contains(body, "exec format error") {
		t.Errorf("expected an error naming the verification and the reason, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_SuccessKeepsThePreviousVersion(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := installedPdx(t)
	// an older .prev already there: it must be replaced by the version that was just running
	if err := os.WriteFile(target+".prev", []byte("older"), 0755); err != nil {
		t.Fatal(err)
	}

	var verified string
	var calls []restartCall
	m := &DevModule{
		repoRoot:   dir,
		selfPathFn: func() (string, error) { return target, nil },
		verifyFn:   func(_ context.Context, p, _ string) error { verified = p; return nil },
		restartFn: func(reason, execPath string, _ func()) error {
			calls = append(calls, restartCall{reason, execPath})
			return nil
		},
	}
	postRebuild(t, m)

	if verified != target+".new" {
		t.Errorf("verified %q, want the new file before the swap", verified)
	}
	if got := readString(t, target); got == "old" || got == "" {
		t.Errorf("target not replaced: %q", got)
	}
	if got := readString(t, target+".prev"); got != "old" {
		t.Errorf(".prev = %q, want the version that was running", got)
	}
	if exists(target + ".new") {
		t.Error(".new left behind")
	}
	if len(calls) != 1 || calls[0].execPath != target {
		t.Errorf("restart calls: %+v", calls)
	}
}

func TestHandleDaemonRebuild_FirstInstallHasNoPrevAndStillSucceeds(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := filepath.Join(t.TempDir(), "pdx")

	m := &DevModule{repoRoot: dir, selfPathFn: func() (string, error) { return target, nil }}
	body := postRebuild(t, m)

	if !exists(target) || exists(target+".prev") {
		t.Errorf("target exists %v, prev exists %v", exists(target), exists(target+".prev"))
	}
	if !strings.Contains(body, `"type":"success"`) {
		t.Errorf("expected success:\n%s", body)
	}
}

func TestHandleDaemonRebuild_RefusedRestartPutsThePreviousVersionBack(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := installedPdx(t)

	m := &DevModule{
		repoRoot:   dir,
		selfPathFn: func() (string, error) { return target, nil },
		restartFn:  func(string, string, func()) error { return core.ErrRestartInProgress },
	}
	body := postRebuild(t, m)

	if got := readString(t, target); got != "old" {
		t.Errorf("target = %q, want the old version back", got)
	}
	if !strings.Contains(body, "restart_in_progress") || !strings.Contains(body, "the previous version was put back") {
		t.Errorf("expected the refusal and the restore note, got:\n%s", body)
	}
	if strings.Contains(body, `"type":"restarting"`) {
		t.Errorf("must not announce a restart:\n%s", body)
	}
}

func TestHandleDaemonRebuild_RefusedRestartOnFirstInstallJustReportsIt(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := filepath.Join(t.TempDir(), "pdx")

	m := &DevModule{
		repoRoot:   dir,
		selfPathFn: func() (string, error) { return target, nil },
		restartFn:  func(string, string, func()) error { return core.ErrRestartInProgress },
	}
	body := postRebuild(t, m)

	if !strings.Contains(body, "restart_in_progress") || strings.Contains(body, "put back") {
		t.Errorf("expected only the refusal, got:\n%s", body)
	}
}

func TestHandleDaemonRebuild_LinkFailureDoesNotSwap(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	target := installedPdx(t)
	// a non-empty directory as .prev cannot be removed by os.Remove, so no way back can be made
	if err := os.MkdirAll(target+".prev", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target+".prev", "x"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	n := 0
	m := &DevModule{
		repoRoot:   dir,
		selfPathFn: func() (string, error) { return target, nil },
		restartFn:  func(string, string, func()) error { n++; return nil },
	}
	body := postRebuild(t, m)

	if got := readString(t, target); got != "old" {
		t.Errorf("target = %q, must stay when no way back could be kept", got)
	}
	if exists(target+".new") || n != 0 {
		t.Errorf(".new exists %v, restarts %d", exists(target+".new"), n)
	}
	if !strings.Contains(body, `"type":"error"`) {
		t.Errorf("expected error:\n%s", body)
	}
}

func TestHandleDaemonRebuild_BuildFailureLeavesNoNewFile(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){ broken syntax }\n")
	target := installedPdx(t)
	// a stale .new from an earlier run must not survive either
	if err := os.WriteFile(target+".new", []byte("stale"), 0755); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{repoRoot: dir, selfPathFn: func() (string, error) { return target, nil }}
	postRebuild(t, m)
	if exists(target + ".new") {
		t.Error(".new left behind after a failed build")
	}
	if got := readString(t, target); got != "old" {
		t.Errorf("target = %q", got)
	}
}

func TestHandleDaemonRebuild_RenameFailureLeavesNoNewFile(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	dir := writeTinyRepo(t, "package main\nfunc main(){}\n")
	// bin/pdx is a non-empty directory: rename(file -> dir) fails
	if err := os.MkdirAll(filepath.Join(dir, "bin", "pdx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "pdx", "blocker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &DevModule{repoRoot: dir}
	body := postRebuild(t, m)
	if !strings.Contains(body, "rename failed") {
		t.Errorf("expected rename failure:\n%s", body)
	}
	if exists(filepath.Join(dir, "bin", "pdx.new")) {
		t.Error(".new left behind after a failed rename")
	}
}

// verifyBinary against a real executable that prints what a pdx prints for `version --json`.
func fakePdx(t *testing.T, body string) string {
	t.Helper()
	dir := writeTinyRepo(t, body)
	out := filepath.Join(t.TempDir(), "fake")
	if err := (&DevModule{repoRoot: dir}).buildBinary(context.Background(), buildTarget{}, "x", "v", out, func(string) {}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerifyBinary(t *testing.T) {
	const prog = `package main
import ("fmt";"os")
func main(){
	if len(os.Args) < 3 || os.Args[1] != "version" || os.Args[2] != "--json" { os.Exit(2) }
	fmt.Print(%s)
}
`
	mk := func(printExpr string) string { return fakePdx(t, strings.Replace(prog, "%s", printExpr, 1)) }
	good := mk("`{\"hash\":\"abc1234\"}`")
	notJSON := mk(`"hello"`)
	failing := fakePdx(t, "package main\nimport \"os\"\nfunc main(){ os.Exit(1) }\n")

	cases := []struct {
		name, path, want string
		ok               bool
	}{
		{"hash matches", good, "abc1234", true},
		{"hash differs", good, "other", false},
		{"empty wanted hash needs only exit 0 and JSON", good, "", true},
		{"exit non-zero", failing, "abc1234", false},
		{"output is not JSON", notJSON, "abc1234", false},
		{"empty wanted hash, still not JSON", notJSON, "", false},
		{"missing file", filepath.Join(t.TempDir(), "nope"), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := verifyBinary(context.Background(), c.path, c.want)
			if (err == nil) != c.ok {
				t.Errorf("verifyBinary err = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestInit_InstallsVerifier(t *testing.T) {
	m := &DevModule{repoRoot: t.TempDir()}
	if err := m.Init(nil); err != nil {
		t.Fatal(err)
	}
	if m.verifyFn == nil {
		t.Fatal("Init left verifyFn nil: a real daemon would swap in an unchecked binary")
	}
}
