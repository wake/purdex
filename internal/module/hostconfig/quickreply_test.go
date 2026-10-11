package hostconfig

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const qrPath = "/api/hostconfig/quick-replies"

func putQR(m *Module, items string, base int) (int, string) {
	rr := serve(m, http.MethodPut, qrPath, `{"items":`+items+`,"baseRevision":`+strconv.Itoa(base)+`}`)
	return rr.Code, rr.Body.String()
}

func getQR(t *testing.T, m *Module) string {
	t.Helper()
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	return string(got["quickReplies"])
}

func TestQuickRepliesV2RoundTripEveryAnswerCarriesText(t *testing.T) {
	m := newTestModule(t)
	code, body := putQR(m, `[{"id":"a","title":" Go ","content":" go on ","text":"ignored"}]`, 0)
	require.Equal(t, 200, code, body)
	want := `{"items":[{"id":"a","title":"Go","content":"go on","text":"go on"}],"revision":1}`
	assert.JSONEq(t, want, body)
	assert.JSONEq(t, want, getQR(t, m))
	code, body = putQR(m, `[]`, 0) // stale
	require.Equal(t, 409, code)
	assert.JSONEq(t, want, body)
}

func TestQuickRepliesStoredPreV2RowIsUpgradedOnRead(t *testing.T) {
	m := newTestModule(t)
	long := strings.Repeat("字", 40) // 120 bytes
	putRow(t, m, KeyQuickReplies, `[{"id":"a","text":"hello"},{"id":"b","text":"`+long+`"}]`)
	var got struct {
		Items []QuickReply `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(getQR(t, m)), &got))
	require.Len(t, got.Items, 2)
	assert.Equal(t, QuickReply{ID: "a", Title: "hello", Content: "hello", Text: "hello"}, got.Items[0])
	assert.Equal(t, strings.Repeat("字", 33), got.Items[1].Title, "cut at 99 bytes, on a rune boundary")
	assert.Equal(t, long, got.Items[1].Content)
	e, _ := m.store.Get(KeyQuickReplies)
	assert.Contains(t, string(e.Value), `"text":"hello"}`, "nothing rewritten on read")
	assert.NotContains(t, string(e.Value), "title")
}

func TestQuickRepliesLegacyPutKeepsStoredTitleByID(t *testing.T) {
	m := newTestModule(t)
	code, _ := putQR(m, `[{"id":"a","title":"Nice","content":"one"},{"id":"b","title":"Other","content":"two"}]`, 0)
	require.Equal(t, 200, code)
	code, body := putQR(m, `[{"id":"b","text":"two!","title":"ignored"},{"id":"a","text":"one!"},{"id":"c","text":"fresh"}]`, 1)
	require.Equal(t, 200, code, body)
	assert.JSONEq(t, `{"items":[
		{"id":"b","title":"Other","content":"two!","text":"two!"},
		{"id":"a","title":"Nice","content":"one!","text":"one!"},
		{"id":"c","title":"fresh","content":"fresh","text":"fresh"}],"revision":2}`, body)
}

func TestQuickRepliesLegacyPutKeepsTitleOfPreV2StoredRow(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyQuickReplies, `[{"id":"a","text":"old text"}]`)
	code, body := putQR(m, `[{"id":"a","text":"new text"}]`, 1)
	require.Equal(t, 200, code, body)
	assert.JSONEq(t, `{"items":[{"id":"a","title":"old text","content":"new text","text":"new text"}],"revision":2}`, body)
}

func TestQuickRepliesLegacyTitleCutOnRuneBoundary(t *testing.T) {
	m := newTestModule(t)
	long := strings.Repeat("字", 40)
	code, body := putQR(m, `[{"id":"a","text":"`+long+`"}]`, 0)
	require.Equal(t, 200, code, body)
	assert.Contains(t, body, `"title":"`+strings.Repeat("字", 33)+`"`)
}

// The loser of a race must get a 409, never a row built from the title it
// read before the winner committed.
func TestQuickRepliesLegacyTitleIsReadInsideTheCAS(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "hc.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	m := &Module{store: s}
	code, _ := putQR(m, `[{"id":"a","title":"Old","content":"x"}]`, 0)
	require.Equal(t, 200, code)

	entered := make(chan struct{})
	var once sync.Once
	s.afterRead = func() {
		once.Do(func() { close(entered); time.Sleep(100 * time.Millisecond) })
	}
	var wg sync.WaitGroup
	var aCode, bCode int
	var bBody string
	wg.Add(2)
	go func() {
		defer wg.Done()
		aCode, _ = putQR(m, `[{"id":"a","title":"New","content":"x"}]`, 1)
	}()
	<-entered
	go func() {
		defer wg.Done()
		bCode, bBody = putQR(m, `[{"id":"a","text":"x"}]`, 1)
	}()
	wg.Wait()
	assert.Equal(t, 200, aCode)
	assert.Equal(t, 409, bCode, bBody)
	assert.Contains(t, bBody, `"title":"New"`)
	e, _ := s.Get(KeyQuickReplies)
	assert.Contains(t, string(e.Value), `"New"`)
	assert.Equal(t, int64(2), e.Revision)
}

// A second legacy PUT from the winner's revision must see the title the
// first PUT stored (the builder gets the current row, not a stale read).
func TestQuickRepliesLegacyPutSeesTitleStoredByEarlierPut(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, 200, mustCode(putQR(m, `[{"id":"a","title":"First","content":"x"}]`, 0)))
	require.Equal(t, 200, mustCode(putQR(m, `[{"id":"a","title":"Second","content":"x"}]`, 1)))
	_, body := putQR(m, `[{"id":"a","text":"y"}]`, 2)
	assert.Contains(t, body, `"title":"Second"`)
}

func mustCode(code int, _ string) int { return code }

func TestQuickRepliesShapeIsDecidedByContentPresence(t *testing.T) {
	m := newTestModule(t)
	// content absent -> legacy, even with a title
	code, body := putQR(m, `[{"id":"a","text":"t","title":"zzz"}]`, 0)
	require.Equal(t, 200, code, body)
	assert.Contains(t, body, `"title":"t"`)
	// both present -> content wins
	code, body = putQR(m, `[{"id":"a","title":"T","content":"C","text":"X"}]`, 1)
	require.Equal(t, 200, code, body)
	assert.JSONEq(t, `{"items":[{"id":"a","title":"T","content":"C","text":"C"}],"revision":2}`, body)
}

func TestQuickRepliesRejects(t *testing.T) {
	cases := map[string]string{
		"content null":           `[{"id":"a","title":"T","content":null}]`,
		"content null with text": `[{"id":"a","title":"T","content":null,"text":"ok"}]`,
		"content number":         `[{"id":"a","title":"T","content":5,"text":"ok"}]`,
		"content object":         `[{"id":"a","title":"T","content":{},"text":"ok"}]`,
		"content empty":          `[{"id":"a","title":"T","content":"","text":"ok"}]`,
		"content blank":          `[{"id":"a","title":"T","content":"  ","text":"ok"}]`,
		"content nul":            `[{"id":"a","title":"T","content":"a\u0000b"}]`,
		"content too long":       `[{"id":"a","title":"T","content":"` + strings.Repeat("x", 1001) + `"}]`,
		"title absent":           `[{"id":"a","content":"c"}]`,
		"title null":             `[{"id":"a","title":null,"content":"c"}]`,
		"title number":           `[{"id":"a","title":3,"content":"c"}]`,
		"title empty":            `[{"id":"a","title":" ","content":"c"}]`,
		"title nul":              `[{"id":"a","title":"a\u0000","content":"c"}]`,
		"title too long":         `[{"id":"a","title":"` + strings.Repeat("x", 101) + `","content":"c"}]`,
		"legacy text null":       `[{"id":"a","text":null}]`,
		"legacy text number":     `[{"id":"a","text":1}]`,
		"neither":                `[{"id":"a"}]`,
		"over 20":                quickReplyList(21),
	}
	for name, items := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModule(t)
			code, body := putQR(m, items, 0)
			assert.Equal(t, 400, code, body)
			e, _ := m.store.Get(KeyQuickReplies)
			assert.Equal(t, int64(0), e.Revision)
		})
	}
}

func TestQuickRepliesLimitsAccepted(t *testing.T) {
	m := newTestModule(t)
	code, body := putQR(m, `[{"id":"a","title":"`+strings.Repeat("x", 100)+`","content":"`+strings.Repeat("y", 1000)+`"}]`, 0)
	assert.Equal(t, 200, code, body)
	code, body = putQR(m, quickReplyList(20), 1)
	assert.Equal(t, 200, code, body)
}

func TestQuickRepliesInvalidUTF8BecomesReplacementChar(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[{"id":"a","title":"t` + string([]byte{0xff}) + `","content":"c` + string([]byte{0xfe}) + `"}],"baseRevision":0}`
	rr := serve(m, http.MethodPut, qrPath, body)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"a","title":"t�","content":"c�","text":"c�"}],"revision":1}`, rr.Body.String())
	e, _ := m.store.Get(KeyQuickReplies)
	assert.Contains(t, string(e.Value), "�")
}

func TestQuickRepliesStaleBaseRevisionIs409WhateverThePayload(t *testing.T) {
	m := newTestModule(t)
	code, _ := putQR(m, `[{"id":"a","title":"T","content":"C"}]`, 0)
	require.Equal(t, 200, code)
	code, body := putQR(m, `[{"id":"a","content":null}]`, 0)
	assert.Equal(t, 409, code, body)
}
