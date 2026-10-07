package nex

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pstore "github.com/wake/purdex/internal/store"
)

type failingNames struct{}

func (failingNames) All(context.Context) (map[string]string, error) {
	return nil, errors.New("names db down")
}

func openRealMeta(t *testing.T) *pstore.MetaStore {
	t.Helper()
	m, err := pstore.OpenMeta(filepath.Join(t.TempDir(), "meta.db"))
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	return m
}

// A transcript with an interactive entrypoint but no prompt and no title.
func (e *convEnv) writePromptless(t *testing.T, sid string, at time.Time) {
	t.Helper()
	e.writeTranscript(t, sid, at, ceLine(t, map[string]any{"type": "assistant", "cwd": e.home, "entrypoint": "cli"}))
}

func TestConversationsHTTP_RegistryNameTitle(t *testing.T) {
	env := newConvEnv(t)
	meta := openRealMeta(t)
	require.Same(t, env.m, env.m.WithConversationNames(meta.ConversationNames()))
	require.NoError(t, meta.ConversationNames().Upsert(context.Background(), ceS1, "purdex-47", 1))
	env.writePromptless(t, ceS1, time.UnixMilli(1_759_700_000_000))
	env.writePromptless(t, ceS2, time.UnixMilli(1_759_700_100_000))

	status, res := env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	require.Len(t, res.Conversations, 2)
	byID := map[string]conversationRow{}
	for _, r := range res.Conversations {
		byID[r.SessionID] = r
	}
	assert.Equal(t, "purdex-47", byID[ceS1].Title)
	assert.Equal(t, "registry", byID[ceS1].TitleSource)
	assert.Equal(t, ceS2[:8], byID[ceS2].Title, "a row without a name keeps the 8-char id")
	assert.Equal(t, "session_id", byID[ceS2].TitleSource)
}

func TestConversationsHTTP_NamesReaderFailureKeepsTheList(t *testing.T) {
	env := newConvEnv(t)
	env.m.WithConversationNames(failingNames{})
	env.writeTranscript(t, ceS1, time.UnixMilli(1_759_700_000_000),
		ceLine(t, cePrompt(env.home, "the prompt line")))
	env.writePromptless(t, ceS2, time.UnixMilli(1_759_700_100_000))

	status, res := env.get(t, "?state=ended")
	require.Equal(t, http.StatusOK, status, res.Error)
	require.Len(t, res.Conversations, 2)
	for _, r := range res.Conversations {
		switch r.SessionID {
		case ceS1:
			assert.Equal(t, "the prompt line", r.Title)
			assert.Equal(t, "prompt", r.TitleSource)
		case ceS2:
			assert.Equal(t, ceS2[:8], r.Title)
			assert.Equal(t, "session_id", r.TitleSource)
		}
	}
	assert.NotEmpty(t, env.logs.find("names db down"), "the failure is logged")
}
