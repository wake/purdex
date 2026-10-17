package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watch"
)

// Every `pdx watch …` command the watcher is told about in a message exists, with every flag the message names (#3071): the messages said
// `pdx watch log --all` and `pdx watch ack <id>… --note` for a release in which neither was there. A rename of a command or a flag now
// fails here instead of leaving the watcher with advice that does not run.
func TestMessagesOnlyNameWatchCommandsAndFlagsThatExist(t *testing.T) {
	at := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	var texts []string
	var recs []watch.Record
	for i, k := range []watch.Kind{watch.KindUnmanagedHeavy, watch.KindPressureHigh, watch.KindSlotOrphaned, watch.KindSlotLong} {
		r := watch.Record{ID: "mlab-" + string(rune('A'+i)), Kind: k, Host: "mlab", At: at, Label: watch.LabelGateGap, Argv: []string{"vitest"}, Detail: "d", Slot: "heavy-1"}
		recs = append(recs, r)
		texts = append(texts, watch.EventText(r), watch.FoldText("mlab", k, []string{r.ID}, 3))
	}
	texts = append(texts, watch.BacklogText("mlab", recs, 1, 1), watch.BacklogText("mlab", recs, 1, 3))

	cmd := regexp.MustCompile(`pdx watch (\w+)([^。；\n]*)`)
	flagRe := regexp.MustCompile(`--([a-z][a-z-]*)`)
	mentioned := map[string]map[string]bool{} // subcommand -> flags
	for _, text := range texts {
		for _, m := range cmd.FindAllStringSubmatch(text, -1) {
			if mentioned[m[1]] == nil {
				mentioned[m[1]] = map[string]bool{}
			}
			for _, f := range flagRe.FindAllStringSubmatch(m[2], -1) {
				mentioned[m[1]][f[1]] = true
			}
		}
	}
	require.Contains(t, mentioned, "log", "the messages do tell the watcher about pdx watch log")
	require.Contains(t, mentioned, "ack", "and about pdx watch ack")

	for sub, flags := range mentioned {
		var out, errb bytes.Buffer
		watchMain([]string{sub, "-h"}, &out, &errb)
		help := errb.String()
		require.False(t, strings.HasPrefix(help, "usage: pdx watch"), "pdx watch %s is not a command: %q", sub, help)
		for f := range flags {
			require.Contains(t, help, "\n  -"+f, "pdx watch %s has no --%s", sub, f)
		}
	}
}
