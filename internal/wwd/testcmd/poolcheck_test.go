package testcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

const labelManifest = `
[tasks.go-changed]
run = "go test {files}"
args = "files"
labels = ["os:darwin", "repo:purdex"]
`

type poolAPI struct {
	*fakeAPI
	pool    wire.Pool
	poolErr error
}

func (a *poolAPI) Pool(context.Context) (wire.Pool, error) { return a.pool, a.poolErr }

func labelRepo(t *testing.T) string {
	t.Helper()
	dir := repo(t, false)
	os.MkdirAll(filepath.Join(dir, ".wwd"), 0o755)
	os.WriteFile(filepath.Join(dir, ".wwd/tasks.toml"), []byte(labelManifest), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "manifest")
	return dir
}

func peer(alias string, reachable, worker bool, owner *wire.OwnerState, labels ...string) wire.Peer {
	p := wire.Peer{Info: wire.Info{HostID: "h-" + alias, Alias: alias, Labels: labels, Worker: wire.InfoWorker{Enabled: worker}}, Reachable: reachable}
	if reachable {
		p.Facts = &wire.Facts{HostID: "h-" + alias, Alias: alias, Owner: owner}
	}
	return p
}

func TestNoHostHoldsTheLabelsIsExit5AndNothingIsQueued(t *testing.T) {
	api := &poolAPI{fakeAPI: &fakeAPI{}, pool: wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, false, nil, "os:darwin"),
		peer("air26", true, true, nil, "os:darwin", "tool:go1.26"),
	}}}
	code, _, errOut := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code != ExitNoHost || api.submitted != 0 {
		t.Fatalf("code %d submitted %d: %s", code, api.submitted, errOut)
	}
	for _, want := range []string{"repo:purdex", "mlab", "air26"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("message lacks %q:\n%s", want, errOut)
		}
	}
}

func TestAnUnreachableHostKeepsItsLastKnownLabelsForTheJudgement(t *testing.T) {
	api := &poolAPI{fakeAPI: &fakeAPI{}, pool: wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, true, nil, "os:darwin"),
		peer("air26", false, true, nil, "os:darwin", "repo:purdex"),
	}}}
	code, _, errOut := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code == ExitNoHost || api.submitted != 1 {
		t.Fatalf("code %d submitted %d: %s", code, api.submitted, errOut)
	}
	if !strings.Contains(errOut, "air26") || !strings.Contains(errOut, "unreachable") {
		t.Errorf("no hint naming the unreachable holder:\n%s", errOut)
	}
}

func TestAHostNeverReachedHoldsNothing(t *testing.T) {
	never := wire.Peer{Info: wire.Info{Alias: "mserv", Labels: []string{}}}
	api := &poolAPI{fakeAPI: &fakeAPI{}, pool: wire.Pool{Peers: []wire.Peer{peer("mlab", true, true, nil, "os:darwin"), never}}}
	code, _, _ := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code != ExitNoHost {
		t.Fatalf("code %d", code)
	}
}

func TestHoldersThatCannotTakeItNowQueueWithAHint(t *testing.T) {
	api := &poolAPI{fakeAPI: &fakeAPI{}, pool: wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, false, nil, "os:darwin", "repo:purdex"),
		peer("air26", true, true, &wire.OwnerState{Active: true}, "os:darwin", "repo:purdex"),
	}}}
	code, _, errOut := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code == ExitNoHost || api.submitted != 1 {
		t.Fatalf("code %d submitted %d: %s", code, api.submitted, errOut)
	}
	for _, want := range []string{"mlab", "worker off", "air26", "owner active"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("hint lacks %q:\n%s", want, errOut)
		}
	}
}

func TestAReadyHolderPrintsNoHint(t *testing.T) {
	api := &poolAPI{fakeAPI: &fakeAPI{}, pool: wire.Pool{Peers: []wire.Peer{
		peer("mlab", true, false, nil, "os:darwin"),
		peer("air26", true, true, nil, "os:darwin", "repo:purdex"),
	}}}
	code, _, errOut := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code == ExitNoHost || api.submitted != 1 || strings.Contains(errOut, "pdx test:") {
		t.Fatalf("code %d submitted %d: %s", code, api.submitted, errOut)
	}
}

func TestAPoolThatCannotBeReadDoesNotStopTheSubmission(t *testing.T) {
	api := &poolAPI{fakeAPI: &fakeAPI{}, poolErr: context.DeadlineExceeded}
	code, _, _ := run(t, Options{Dir: labelRepo(t), Args: args(t, "go-changed", "a.test.ts"), API: api})
	if code == ExitNoHost || api.submitted != 1 {
		t.Fatalf("code %d submitted %d", code, api.submitted)
	}
}
