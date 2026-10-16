package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var tAwake = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func awakeSince(id, sess string, since time.Time) HolderAnswer {
	return HolderAnswer{HostID: id, State: HolderAwake, SessionID: sess, Since: since}
}

// Spec §12.3 "Two awake at once": the watcher awake the longest (the earliest since) wins; on a tie (the same second) the smaller
// host id; one that cannot say since is the youngest.
func TestEarlier_TheRule(t *testing.T) {
	a := awakeSince("a:1", "sa", tAwake)
	b := awakeSince("b:1", "sb", tAwake.Add(time.Minute))
	require.True(t, Earlier(a, b))
	require.False(t, Earlier(b, a))

	// the same second (the sub-second part is not compared): the smaller host id
	c := awakeSince("c:1", "sc", tAwake.Add(400*time.Millisecond))
	require.True(t, Earlier(a, c) && !Earlier(c, a))
	d := awakeSince("0:1", "sd", tAwake.Add(900*time.Millisecond))
	require.True(t, Earlier(d, a) && !Earlier(a, d), "equal second, smaller id wins")

	// no since (an older daemon): the youngest
	old := HolderAnswer{HostID: "0:0", State: HolderAwake, SessionID: "so"}
	require.True(t, Earlier(b, old))
	require.False(t, Earlier(old, b))
	// two without since: the smaller host id
	old2 := HolderAnswer{HostID: "z:9", State: HolderAwake, SessionID: "sz"}
	require.True(t, Earlier(old, old2) && !Earlier(old2, old))

	// it is a strict order: nothing wins over itself
	require.False(t, Earlier(a, a))
}

// The locator picks among several awake answers by that rule, not by the order of targets, and two locators with targets in
// opposite orders pick the same.
func TestLocate_SeveralAwakePickByTheRuleWhateverTheOrderOfTargets(t *testing.T) {
	hold := func() map[string]HolderAnswer {
		return map[string]HolderAnswer{
			"air26": awakeSince("a:1", "s-a", tAwake.Add(time.Hour)),
			"mlab":  awakeSince("m:1", "s-m", tAwake),
		}
	}
	for _, order := range [][]string{{"air26", "mlab"}, {"mlab", "air26"}} {
		f := &fakeHosts{hold: hold()}
		l, _ := newLoc(t, f)
		w, err := l.Current(context.Background(), order, true)
		require.NoError(t, err)
		require.Equal(t, "mlab", w.Alias, "mlab has been awake longer, whichever target is listed first: %v", order)
		require.True(t, w.Since.Equal(tAwake), "the pick carries its since")
	}
	// equal since → the smaller host id
	for _, order := range [][]string{{"air26", "mlab"}, {"mlab", "air26"}} {
		f := &fakeHosts{hold: map[string]HolderAnswer{
			"air26": awakeSince("z:1", "s-a", tAwake),
			"mlab":  awakeSince("m:1", "s-m", tAwake),
		}}
		l, _ := newLoc(t, f)
		w, _ := l.Current(context.Background(), order, true)
		require.Equal(t, "mlab", w.Alias, "m:1 < z:1")
	}
}

// An announce of a watcher that beats the remembered one makes the locator look again; one that does not, or is the remembered
// watcher itself, changes nothing.
func TestLocator_PreferForgetsARememberedWatcherThatTheAnnouncedOneBeats(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": awakeSince("a:1", "s-a", tAwake.Add(time.Hour)),
		"mlab":  {HostID: "m:1", State: HolderNone},
	}}
	l, _ := newLoc(t, f)
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "air26", w.Alias)

	// the same watcher announced again: nothing changes
	l.Prefer(awakeSince("a:1", "s-a", tAwake.Add(time.Hour)))
	cur, _ := l.Remembered()
	require.NotNil(t, cur)

	// a younger one: nothing changes
	l.Prefer(awakeSince("m:1", "s-m", tAwake.Add(2*time.Hour)))
	cur, _ = l.Remembered()
	require.NotNil(t, cur, "a watcher that is younger does not displace the remembered one")

	// an older one beats it: the remembered one is forgotten, the next search finds the pick (and keeps it as the last)
	f.hold["mlab"] = awakeSince("m:1", "s-m", tAwake)
	l.Prefer(awakeSince("m:1", "s-m", tAwake))
	cur, last := l.Remembered()
	require.Nil(t, cur)
	require.NotNil(t, last)
	w, err = l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "mlab", w.Alias)

	// something that is not awake never displaces anything
	l.Prefer(HolderAnswer{HostID: "x:1", State: HolderResting, SessionID: "s", Since: tAwake.Add(-time.Hour)})
	cur, _ = l.Remembered()
	require.NotNil(t, cur)
}

// The host of the remembered watcher announces another session (it restarted its watcher): the remembered one is an earlier run
// of it and is forgotten at once, although the new one is younger.
func TestLocator_PreferReplacesAWatcherOfTheSameHostWithAnotherSession(t *testing.T) {
	f := &fakeHosts{hold: map[string]HolderAnswer{
		"air26": awakeSince("a:1", "s-old", tAwake),
		"mlab":  {HostID: "m:1", State: HolderNone},
	}}
	l, _ := newLoc(t, f)
	_, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)

	l.Prefer(awakeSince("a:1", "s-new", tAwake.Add(time.Hour)))
	cur, last := l.Remembered()
	require.Nil(t, cur, "the old session of that host is not trusted any more")
	require.NotNil(t, last)

	f.hold["air26"] = awakeSince("a:1", "s-new", tAwake.Add(time.Hour))
	w, err := l.Current(context.Background(), targets, true)
	require.NoError(t, err)
	require.Equal(t, "s-new", w.SessionID)
}
