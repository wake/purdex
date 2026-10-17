package resourcesmod

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

// A new subscriber gets the state once on connect (HandleHostEvents calls the OnSubscribe callback).
func TestEvents_SnapshotOnSubscribe(t *testing.T) {
	f := newStoreFix(t)
	f.m.core = &core.Core{Events: core.NewEventsBroadcaster()}
	f.m.core.Events.OnSubscribe(f.m.sendEventSnapshot)
	late := f.m.core.Events.AddTestSubscriber()
	defer f.m.core.Events.RemoveTestSubscriber(late)
	f.m.sendEventSnapshot(late) // what HandleHostEvents does for a connection
	select {
	case b := <-late.SendCh():
		var ev core.HostEvent
		if json.Unmarshal(b, &ev) != nil || ev.Type != resources.EventType {
			t.Fatalf("snapshot = %s", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot on subscribe")
	}
}
