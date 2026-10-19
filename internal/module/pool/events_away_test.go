package poolmod

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// wwd that was there and goes away: every host is announced (the App, which stopped polling, must read the error), and when wwd is back
// every host is announced again, also the one that changed while it was away. Otherwise an App whose connection to this daemon did not drop
// would keep the old pool until something else changed.
func TestPoolChanged_WWDAwayAndBackAnnouncesEveryHost(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wwd")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	lp := newLivePool(t)
	serve := func() *http.Server {
		ln, err := net.Listen("unix", sock)
		require.NoError(t, err)
		srv := &http.Server{Handler: lp.handler()}
		go srv.Serve(ln)
		return srv
	}
	srv := serve()
	t.Cleanup(func() { srv.Close() })
	_, n := watching(t, client.New(sock))
	time.Sleep(6 * testPoll) // the baseline
	require.Empty(t, n.all())

	var aliases []string
	for _, h := range hostsOf(lp.get()).Hosts {
		aliases = append(aliases, h.Info.Alias)
	}
	require.NotEmpty(t, aliases)
	count := func(alias string) int {
		c := 0
		for _, h := range n.hosts() {
			if h == alias {
				c++
			}
		}
		return c
	}

	srv.Close() // wwd goes away
	require.Eventually(t, func() bool {
		for _, a := range aliases {
			if count(a) < 1 {
				return false
			}
		}
		return true
	}, 3*time.Second, testPoll, "every host is announced when wwd goes away")
	time.Sleep(6 * testPoll)
	for _, a := range aliases {
		assert.Equal(t, 1, count(a), "once, not at every poll while wwd is away: %s", a)
	}

	lp.change(func(p *wire.Pool) { p.Peers[0].Facts.Worker.Enabled = false }) // it changed while wwd was away
	srv = serve()
	t.Cleanup(func() { srv.Close() })
	require.Eventually(t, func() bool {
		for _, a := range aliases {
			if count(a) < 2 {
				return false
			}
		}
		return true
	}, 3*time.Second, testPoll, "every host is announced again when wwd is back")
}
