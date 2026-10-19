package report

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// The summary carries the passed-test digest only while the pool allows it (Config.Digest, wired to wire.PoolAtLeast(peers, 3) in wwd):
// when a host of the pool could reject the field, or nothing says it may be written, the summary has none.
func TestTheSummaryCarriesTheDigestOnlyWhileThePoolAllowsIt(t *testing.T) {
	events := `{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0.1}
`
	for name, tc := range map[string]struct {
		digest func() bool
		want   bool
	}{
		"the pool allows it": {digest: func() bool { return true }, want: true},
		"the pool does not":  {digest: func() bool { return false }},
		"nothing is wired":   {digest: nil},
	} {
		dir := t.TempDir()
		stdout := filepath.Join(dir, "stdout")
		require.NoError(t, os.WriteFile(stdout, []byte(events), 0o600))
		h := newHarness(t, Config{Digest: tc.digest})
		e := ended(runner.KindRan, run.Result{Outcome: run.Exited, StdoutPath: stdout, Tail: []byte("ok\n")})
		e.Plan = runner.Plan{Summary: "go-json", OutDir: dir}
		e.Root = dir
		h.fin(context.Background(), e)
		require.Len(t, h.s.results, 1, name)
		s := h.s.results[0].Summary
		require.NotNil(t, s, name)
		assert.Equal(t, tc.want, s.PassedDigest != "", name)
	}
}
