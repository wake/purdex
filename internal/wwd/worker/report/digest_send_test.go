package report

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// The result is retried for minutes. If the pool stops allowing the digest between the tries (a host rolled back to a version that
// rejects the field), the digest is not sent the next time: it is decided when the result is sent, not only when the summary was built.
func TestTheDigestIsDroppedFromARetryWhenThePoolNoLongerAllowsIt(t *testing.T) {
	events := `{"Action":"pass","Package":"p","Test":"TestA"}` + "\n"
	dir := t.TempDir()
	stdout := filepath.Join(dir, "stdout")
	require.NoError(t, os.WriteFile(stdout, []byte(events), 0o600))
	asked := 0
	h := newHarness(t, Config{Digest: func() bool { asked++; return asked <= 2 }}) // the summary, the first send: yes; the retry: no
	h.s.errs = []error{errors.New("down")}                                         // the first send fails
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, StdoutPath: stdout, Tail: []byte("ok\n")})
	e.Plan = runner.Plan{Summary: "go-json", OutDir: dir}
	e.Root = dir
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	s := h.s.results[0].Summary
	require.NotNil(t, s)
	assert.Equal(t, 1, s.Passed, "the summary itself is the same")
	assert.Empty(t, s.PassedDigest, "but the retry no longer carries the digest")
}

// A report over the limit is read one byte past it, so the parsers see that it is too long and refuse it: a prefix that happens to end
// on a complete line is not the report, and gets no digest.
func TestAReportOverTheLimitIsRefusedNotReadAsAPrefix(t *testing.T) {
	dir := t.TempDir()
	stdout := filepath.Join(dir, "stdout")
	body := append([]byte(`{"Action":"pass","Package":"p","Test":"TestA"}`+"\n"), bytes.Repeat([]byte("x"), maxReport+10)...)
	require.NoError(t, os.WriteFile(stdout, body, 0o600))
	assert.Len(t, readCapped(stdout), maxReport+1, "one byte past the limit is read, no more")

	h := newHarness(t, Config{Digest: func() bool { return true }})
	e := ended(runner.KindRan, run.Result{Outcome: run.Exited, StdoutPath: stdout, Tail: []byte("ok\n")})
	e.Plan = runner.Plan{Summary: "go-json", OutDir: dir}
	e.Root = dir
	h.fin(context.Background(), e)
	require.Len(t, h.s.results, 1)
	s := h.s.results[0].Summary
	require.NotNil(t, s)
	assert.Empty(t, s.PassedDigest, "a report that was cut has no digest")
	assert.Zero(t, s.Passed, "and is not counted as a prefix either")
}
