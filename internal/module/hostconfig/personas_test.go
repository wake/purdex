package hostconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/personas"
)

func uri(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func pngBytes(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	if noise {
		r := rand.New(rand.NewSource(1))
		for i := range img.Pix {
			img.Pix[i] = uint8(r.Intn(256))
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil))
	return buf.Bytes()
}

// webpLossless is the header of a lossless WebP of the given size (the daemon reads the header only).
func webpLossless(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x05\x00\x00\x00\x2f\x00\x00\x00\x00")
	bits := uint32(w-1) | uint32(h-1)<<14
	binary.LittleEndian.PutUint32(b[21:25], bits)
	return b
}

func webpExtended(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	put24 := func(at, v int) { b[at], b[at+1], b[at+2] = byte(v), byte(v>>8), byte(v>>16) }
	put24(24, w-1)
	put24(27, h-1)
	return b
}

func webpLossy(w, h int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBPVP8 \x0a\x00\x00\x00\x00\x00\x00\x9d\x01\x2a\x00\x00\x00\x00")
	binary.LittleEndian.PutUint16(b[26:28], uint16(w))
	binary.LittleEndian.PutUint16(b[28:30], uint16(h))
	return b
}

func persona(code string) map[string]any {
	return map[string]any{"code": code, "name": strings.ToUpper(code[:1]) + code[1:], "sex": "male"}
}

func putPersonas(m *Module, items any, base int64) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"items": items, "baseRevision": base})
	return serve(m, http.MethodPut, "/api/hostconfig/personas", string(body))
}

func personasModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	m.core = core.New(core.CoreDeps{Config: &config.Config{HostID: "mlab"}})
	return m
}

func TestPersonas_GetEmpty(t *testing.T) {
	m := personasModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig/personas", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[],"stamp":{"at":0,"host":""},"revision":0}`, rr.Body.String())
}

func TestPersonas_PutStampsWithTheHostAndTheClock(t *testing.T) {
	m := personasModule(t)
	m.personaNow = func() int64 { return 1760000000000 }
	rr := putPersonas(m, []any{persona("hiro")}, 0)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"revision":1}`, rr.Body.String())

	got := serve(m, http.MethodGet, "/api/hostconfig/personas", "")
	var body struct {
		Items    []personas.Persona `json:"items"`
		Stamp    personas.Stamp     `json:"stamp"`
		Revision int64              `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, "hiro", body.Items[0].Code)
	assert.Equal(t, personas.Stamp{At: 1760000000000, Host: "mlab"}, body.Stamp)
	assert.EqualValues(t, 1, body.Revision)
}

func TestPersonas_StaleBaseIs409WithTheCurrentEntry(t *testing.T) {
	m := personasModule(t)
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro")}, 0).Code)
	rr := putPersonas(m, []any{persona("anna")}, 0)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	var body struct {
		Items    []personas.Persona `json:"items"`
		Revision int64              `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	assert.Equal(t, "hiro", body.Items[0].Code)
	assert.EqualValues(t, 1, body.Revision)
}

// A bad item is 400 {error, index, field}, and nothing is stored.
// Mutation gate: skip any one of these checks -> that row is accepted -> red.
func TestPersonas_BadItemsAreRefusedWithTheirIndexAndField(t *testing.T) {
	retired := persona("hiro")
	retired["retired"] = true
	longName := persona("anna")
	longName["name"] = strings.Repeat("あ", 25)
	badSex := persona("anna")
	badSex["sex"] = "x"
	wordy := persona("anna")
	wordy["personality"] = map[string]any{"zh": "", "en": strings.Repeat("w ", 51)}
	zhLong := persona("anna")
	zhLong["interests"] = map[string]any{"zh": strings.Repeat("字", 11), "en": ""}
	svg := persona("anna")
	svg["avatar_data"] = uri("image/svg+xml", []byte("<svg onload=alert(1)/>"))
	notB64 := persona("anna")
	notB64["avatar_data"] = "data:image/png;base64,@@@@"
	gif := persona("anna")
	gif["avatar_data"] = uri("image/gif", []byte("GIF89a"))
	wrongMagic := persona("anna")
	wrongMagic["avatar_data"] = uri("image/png", jpegBytes(t, 8, 8))
	big := persona("anna")
	big["avatar_data"] = uri("image/png", pngBytes(t, 129, 128, false))
	tall := persona("anna")
	tall["avatar_data"] = uri("image/webp", webpLossless(128, 200))
	huge := persona("anna")
	huge["avatar_data"] = uri("image/png", append(pngBytes(t, 8, 8, false), bytes.Repeat([]byte{0}, 33*1024)...))
	bigRecipe := persona("anna")
	bigRecipe["avatar"] = map[string]any{"source": "x", "options": strings.Repeat("a", 2100)}

	cases := []struct {
		name  string
		items []any
		index int
		field string
	}{
		{"code too short", []any{persona("hi")}, 0, "code"},
		{"code too long", []any{persona("abcdefghijklm")}, 0, "code"},
		{"code with a digit", []any{persona("hir0")}, 0, "code"},
		{"code in capitals", []any{map[string]any{"code": "Hiro", "name": "Hiro"}}, 0, "code"},
		{"duplicate code", []any{persona("hiro"), persona("hiro")}, 1, "code"},
		{"a retired code reused by another persona", []any{retired, persona("hiro")}, 1, "code"},
		{"empty name", []any{map[string]any{"code": "hiro", "name": "  "}}, 0, "name"},
		{"name over 24 runes", []any{longName}, 0, "name"},
		{"unknown sex", []any{badSex}, 0, "sex"},
		{"personality over 50 words", []any{wordy}, 0, "personality"},
		{"interests over 10 runes", []any{zhLong}, 0, "interests"},
		{"svg picture", []any{svg}, 0, "avatar_data"},
		{"not base64", []any{notB64}, 0, "avatar_data"},
		{"a gif", []any{gif}, 0, "avatar_data"},
		{"magic bytes of another format", []any{wrongMagic}, 0, "avatar_data"},
		{"a picture wider than 128", []any{big}, 0, "avatar_data"},
		{"a webp taller than 128", []any{tall}, 0, "avatar_data"},
		{"a picture string over 32 KB", []any{huge}, 0, "avatar_data"},
		{"a recipe over 2 KB", []any{bigRecipe}, 0, "avatar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := personasModule(t)
			rr := putPersonas(m, c.items, 0)
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			var e struct {
				Error string `json:"error"`
				Index int    `json:"index"`
				Field string `json:"field"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &e))
			assert.NotEmpty(t, e.Error)
			assert.Equal(t, c.index, e.Index)
			assert.Equal(t, c.field, e.Field)
			got := serve(m, http.MethodGet, "/api/hostconfig/personas", "")
			assert.JSONEq(t, `{"items":[],"stamp":{"at":0,"host":""},"revision":0}`, got.Body.String(), "nothing stored")
		})
	}
}

// Deleting retires (spec L5): a stored code stays in the collection for good, so no other persona can take it, and a
// restore is a PUT that sets retired back to false.
// Mutation gate: skip the retained-codes check -> the second PUT is accepted -> red.
func TestPersonas_AStoredCodeIsRetiredNotRemoved(t *testing.T) {
	m := personasModule(t)
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro"), persona("anna")}, 0).Code)

	rr := putPersonas(m, []any{persona("anna")}, 1) // hiro left out
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	var e struct{ Field string }
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &e))
	assert.Equal(t, "code", e.Field)
	assert.Contains(t, rr.Body.String(), "hiro")

	retired := persona("hiro")
	retired["retired"] = true
	require.Equal(t, http.StatusOK, putPersonas(m, []any{retired, persona("anna"), persona("freya")}, 1).Code)
	assert.Equal(t, http.StatusBadRequest, putPersonas(m, []any{persona("anna"), persona("freya")}, 2).Code, "a retired code is not removable either")
	assert.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro"), persona("anna"), persona("freya")}, 2).Code, "a restore is allowed")
}

func TestPersonas_MoreThan64AreRefused(t *testing.T) {
	m := personasModule(t)
	items := make([]any, 0, 65)
	for i := 0; i < 65; i++ {
		items = append(items, persona(fmt.Sprintf("p%s", strings.Repeat("a", 1)+string(rune('a'+i%26))+string(rune('a'+i/26)))))
	}
	rr := putPersonas(m, items, 0)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	assert.Equal(t, http.StatusOK, putPersonas(m, items[:64], 0).Code)
}

func TestPersonas_PicturesOfThe3SupportedFormatsAreKept(t *testing.T) {
	for name, data := range map[string]string{
		"png":           uri("image/png", pngBytes(t, 128, 128, false)),
		"jpeg":          uri("image/jpeg", jpegBytes(t, 64, 64)),
		"webp lossless": uri("image/webp", webpLossless(128, 128)),
		"webp extended": uri("image/webp", webpExtended(128, 128)),
		"webp lossy":    uri("image/webp", webpLossy(100, 100)),
		"none":          "",
	} {
		t.Run(name, func(t *testing.T) {
			m := personasModule(t)
			p := persona("hiro")
			p["avatar_data"] = data
			p["avatar"] = map[string]any{"source": "open-peeps", "seed": "Hiro", "options": map[string]any{}}
			rr := putPersonas(m, []any{p}, 0)
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			s, err := m.Personas()
			require.NoError(t, err)
			require.Len(t, s.Items, 1)
			assert.Equal(t, data, s.Items[0].AvatarData)
		})
	}
}

// Only this route takes up to 4 MiB; a hostconfig collection still stops at 1 MiB.
// Mutation gate: give the personas route the default cap -> red; give every route 4 MiB -> red.
func TestPersonas_OnlyThisRouteTakesFourMiB(t *testing.T) {
	m := personasModule(t)
	pic := uri("image/png", pngBytes(t, 128, 128, true))
	require.Less(t, len(pic), 32*1024, "a valid picture string")
	items := make([]any, 0, 64)
	for i := 0; i < 64; i++ {
		p := persona("a" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		p["avatar_data"] = pic
		items = append(items, p)
	}
	body, _ := json.Marshal(map[string]any{"items": items, "baseRevision": 0})
	require.Greater(t, len(body), 1<<20, "the body is over the default cap")
	require.Less(t, len(body), 4<<20)
	rr := serve(m, http.MethodPut, "/api/hostconfig/personas", string(body))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String()[:min(200, rr.Body.Len())])

	over := strings.Repeat("x", 4<<20+1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, serve(m, http.MethodPut, "/api/hostconfig/personas", over).Code)

	other := `{"items":[],"baseRevision":0,"pad":"` + strings.Repeat("x", 1<<20) + `"}`
	assert.Equal(t, http.StatusRequestEntityTooLarge, serve(m, http.MethodPut, "/api/hostconfig/projects", other).Code)
}

// The full read stays small: personas is not in it.
// Mutation gate: add personas to the GET-all -> red.
func TestPersonas_NotInTheFullRead(t *testing.T) {
	m := personasModule(t)
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro")}, 0).Code)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.NotContains(t, got, "personas")
	assert.NotContains(t, rr.Body.String(), "hiro")
}

func TestPersonas_PutAnnouncesAnEventAndNotifiesReaders(t *testing.T) {
	m := personasModule(t)
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	changed := 0
	cancel := m.OnPersonasChange(func() { changed++ })
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro")}, 0).Code)
	assert.Equal(t, 1, changed)

	var ev core.HostEvent
	select {
	case msg := <-sub.SendCh():
		require.NoError(t, json.Unmarshal(msg, &ev))
	default:
		t.Fatal("no event")
	}
	assert.Equal(t, "hostconfig.personas", ev.Type)
	assert.JSONEq(t, `{"revision":1}`, ev.Value)

	// a refused or stale PUT is not a change
	require.Equal(t, http.StatusBadRequest, putPersonas(m, []any{persona("hi")}, 1).Code)
	require.Equal(t, http.StatusConflict, putPersonas(m, []any{persona("anna")}, 0).Code)
	assert.Equal(t, 1, changed)
	cancel()
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro"), persona("anna")}, 1).Code)
	assert.Equal(t, 1, changed, "a cancelled reader hears nothing")
}

// Mutation gate: refuse the Reader on the module -> the registry value does not satisfy personas.Reader -> red.
func TestPersonas_TheModuleIsTheRegisteredReader(t *testing.T) {
	var _ personas.Reader = (*Module)(nil)
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), HostID: "mlab"}, Registry: core.NewServiceRegistry()})
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Close() })
	v, ok := c.Registry.Get(personas.Key)
	require.True(t, ok)
	_, isReader := v.(personas.Reader)
	assert.True(t, isReader)
}
