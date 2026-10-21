package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// S3-3a: the alert list is read one alert at a time. A kind or a level this build does not know is a rule of a later release: it is left
// out and said once, and the rest of the list comes through. Everything else about the list is as strict as it was.

const alertAt = `"since":"2026-10-19T04:00:00Z","last":"2026-10-19T04:01:00Z","cleared_at":null`

func alertJSON(key, kind, level string) string {
	return fmt.Sprintf(`{"key":%q,"kind":%q,"level":%q,%s}`, key, kind, level, alertAt)
}

func alertsServer(t *testing.T, body *string) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(*body)) })
	return New(fakeWWD(t, mux))
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) f(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func TestAlerts_LeavesOutAnUnknownKindAndAnUnknownLevelAndSaysSoOnce(t *testing.T) {
	body := `{"host":"air26","alerts":[` + strings.Join([]string{
		alertJSON("a", "thrash", "digest"),
		alertJSON("b", "meltdown", "push"),   // a kind of a later release
		alertJSON("c", "disk_low", "urgent"), // a level of a later release
		alertJSON("d", "queue_stalled", "watch"),
	}, ",") + `]}`
	c := alertsServer(t, &body)
	var l logs
	c.Logf = l.f

	list, known, err := c.Alerts(context.Background(), "air26")
	if err != nil || !known {
		t.Fatalf("known=%v err=%v", known, err)
	}
	if len(list.Alerts) != 2 || list.Alerts[0].Key != "a" || list.Alerts[1].Key != "d" {
		t.Fatalf("the rest of the list comes through, in order: %+v", list.Alerts)
	}
	if len(l.lines) != 2 || !strings.Contains(l.lines[0], `kind "meltdown"`) || !strings.Contains(l.lines[1], `level "urgent"`) {
		t.Fatalf("one line for each: %q", l.lines)
	}

	// the daemon reads the list again every 30 seconds: not said again
	if _, _, err := c.Alerts(context.Background(), "air26"); err != nil {
		t.Fatal(err)
	}
	if len(l.lines) != 2 {
		t.Fatalf("said again: %q", l.lines)
	}
	// a new unknown kind is said
	body = `{"host":"air26","alerts":[` + alertJSON("e", "tidal_wave", "push") + `]}`
	if _, _, err := c.Alerts(context.Background(), "air26"); err != nil {
		t.Fatal(err)
	}
	if len(l.lines) != 3 || !strings.Contains(l.lines[2], `kind "tidal_wave"`) {
		t.Fatalf("a new kind is said once: %q", l.lines)
	}
}

func TestAlerts_AllUnknownIsAnEmptyListThatTheHostVouchedFor(t *testing.T) {
	body := `{"host":"air26","alerts":[` + alertJSON("b", "meltdown", "push") + `]}`
	c := alertsServer(t, &body)
	c.Logf = func(string, ...any) {}
	list, known, err := c.Alerts(context.Background(), "")
	if err != nil || !known || list.Host != "air26" || list.Alerts == nil || len(list.Alerts) != 0 {
		t.Fatalf("list=%+v known=%v err=%v", list, known, err)
	}
}

// An unknown kind does not excuse what else is wrong: the first thing wrong that is not an unknown kind refuses the whole list.
func TestAlerts_ABrokenAlertNextToAnUnknownOneStillRefusesTheList(t *testing.T) {
	for name, items := range map[string][]string{
		"an unknown kind and a missing key":     {alertJSON("b", "meltdown", "push"), alertJSON("", "thrash", "push")},
		"a missing key, unknown kind":           {alertJSON("", "meltdown", "push")}, // the key is checked before the kind
		"an unknown kind and last before since": {`{"key":"b","kind":"meltdown","level":"push","since":"2026-10-19T04:00:00Z","last":"2026-10-19T03:00:00Z","cleared_at":null}`},
		"an unknown level and a bad detail":     {`{"key":"b","kind":"thrash","level":"urgent","detail":"x\u0007y",` + alertAt + `}`},
		"a bad detail":                          {`{"key":"a","kind":"thrash","level":"push","detail":"x\u0007y",` + alertAt + `}`},
	} {
		body := `{"host":"air26","alerts":[` + strings.Join(items, ",") + `]}`
		c := alertsServer(t, &body)
		c.Logf = func(string, ...any) {}
		if _, _, err := c.Alerts(context.Background(), ""); !errors.Is(err, ErrBadResponse) {
			t.Errorf("%s: err = %v, want ErrBadResponse", name, err)
		}
	}
}

func TestAlerts_TheFourQueueKindsAreKnown(t *testing.T) {
	body := `{"host":"mlab","alerts":[` + strings.Join([]string{
		alertJSON("queue_stalled", "queue_stalled", "watch"),
		alertJSON("job_requeued:j_1", "job_requeued", "watch"),
		alertJSON("queue_backlog", "queue_backlog", "watch"),
		alertJSON("queue_unclaimable:j_2", "queue_unclaimable", "watch"),
	}, ",") + `]}`
	c := alertsServer(t, &body)
	var l logs
	c.Logf = l.f
	list, _, err := c.Alerts(context.Background(), "")
	if err != nil || len(list.Alerts) != 4 || len(l.lines) != 0 {
		t.Fatalf("list=%+v err=%v log=%q", list.Alerts, err, l.lines)
	}
}

// Only the alert list is lenient: another answer with something wrong in it is refused whole, as it was.
func TestOtherAnswersAreStillCheckedWhole(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"host_id":"","alias":"x"}`)) })
	if _, err := New(fakeWWD(t, mux)).Info(context.Background()); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("an info without a host id: err = %v", err)
	}
}
