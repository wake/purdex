package backup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
)

func TestBackupDoneBroadcast(t *testing.T) {
	c := &core.Core{Events: core.NewEventsBroadcaster()}
	m := &BackupModule{core: c, store: openTestBackupStore(t)}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)

	sub := c.Events.AddTestSubscriber()
	defer c.Events.RemoveTestSubscriber(sub)

	content := []byte("hello")
	h := sha256hex(content)
	require.NoError(t, m.store.PutBlob(h, content))
	entry := ManifestEntry{Path: "a.txt", Kind: "file", Hash: h, Size: int64(len(content))}

	post := func(trigger string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"storeId": storeID, "device": "macbook:a3f9d2", "trigger": trigger,
			"manifest": []ManifestEntry{entry},
		})
		req := httptest.NewRequest("POST", "/api/backup/snapshot", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec
	}

	// A committed (written) snapshot broadcasts exactly one backup:done.
	rec := post("auto")
	var resp struct {
		SnapshotID    int64 `json:"snapshotId"`
		CurrentHeadID int64 `json:"currentHeadId"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	select {
	case msg := <-sub.SendCh():
		var ev core.HostEvent
		require.NoError(t, json.Unmarshal(msg, &ev))
		require.Equal(t, "backup:done", ev.Type)
		require.Equal(t, "", ev.Session)

		var p struct {
			StoreID       string `json:"storeId"`
			SnapshotID    int64  `json:"snapshotId"`
			CurrentHeadID int64  `json:"currentHeadId"`
			Device        string `json:"device"`
			Trigger       string `json:"trigger"`
			CreatedAt     int64  `json:"createdAt"`
		}
		require.NoError(t, json.Unmarshal([]byte(ev.Value), &p))
		require.Equal(t, storeID, p.StoreID)
		require.Equal(t, resp.SnapshotID, p.SnapshotID)
		require.Equal(t, resp.CurrentHeadID, p.CurrentHeadID)
		require.Equal(t, "macbook:a3f9d2", p.Device)
		require.Equal(t, "auto", p.Trigger)
		require.NotZero(t, p.CreatedAt)
	default:
		t.Fatal("expected a backup:done broadcast")
	}

	// Exactly one — no further message from the written post.
	select {
	case <-sub.SendCh():
		t.Fatal("expected exactly one broadcast")
	default:
	}

	// A no-op-suppressed post broadcasts nothing — for both auto and pre-restore.
	post("auto")
	post("pre-restore")
	select {
	case <-sub.SendCh():
		t.Fatal("no-op post must not broadcast")
	default:
	}
}
