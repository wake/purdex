package resourcesmod

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/modevents"
)

// A notice that finds no mod says why in the log, once per slot: the streams of the session with the age of their last
// batch and of their capability announcement (a 61 s user_alert on a session with a live stream could not be explained).
func TestFallback_NoModLogsTheStreamsOfTheSession(t *testing.T) {
	lead := &fakeLead{member: true, reached: func(int) bool { return false }}
	f, _, _ := fallbackFix(t, lead)
	var lines []string
	f.m.logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	posID, _ := f.queuedThenOffered(newCID(), pidA, "go build")
	sid := f.position(posID).SessionID
	at := f.m.now().Add(-40 * time.Second)
	f.m.modStreams = func(string) []modevents.StreamInfo {
		return []modevents.StreamInfo{
			{Stream: "stale-one", SID: sid, ModVersion: "1.0.0-alpha.736", AppliedAt: at, CapsAt: at, Caps: []string{"lease.notice.v1"}},
			{Stream: "elsewhere", SID: "other"},
		}
	}
	f.notices()
	f.advance(noticeRetryOther)
	f.notices() // the lead stays unreachable: the slot retries
	var found []string
	for _, l := range lines {
		if strings.Contains(l, "found no mod") {
			found = append(found, l)
		}
	}
	require.Len(t, found, 1, "once per slot, not once per retry")
	assert.Contains(t, found[0], sid)
	assert.Contains(t, found[0], "stream stale-one mod 1.0.0-alpha.736")
	assert.Contains(t, found[0], "caps 40s ago [lease.notice.v1]")
	assert.NotContains(t, found[0], "elsewhere")
}

func TestDescribeModStreams_NoStreamOfTheSession(t *testing.T) {
	f := newPosFix(t)
	f.m.modStreams = func(string) []modevents.StreamInfo {
		return []modevents.StreamInfo{{Stream: "a", SID: "x"}, {Stream: "b", SID: "y", Ended: true}}
	}
	assert.Equal(t, "no stream has this session (1 other live streams)", f.m.describeModStreams("sid-none"))
}
