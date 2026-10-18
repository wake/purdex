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
	"time"

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

// webpChunk is one RIFF chunk: fourcc, size, payload, padded to an even length.
func webpChunk(fourcc string, payload []byte) []byte {
	b := append([]byte(fourcc), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(payload)))
	b = append(b, payload...)
	if len(payload)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// webpFile is a RIFF/WEBP container of the chunks, with the right RIFF size.
func webpFile(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, c := range chunks {
		body = append(body, c...)
	}
	b := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(body)))
	return append(b, body...)
}

// The WebP builders make the headers of the three layouts of the given size, well formed (sizes agree); the picture data is
// not real, as the daemon reads the header only.
func webpLosslessChunk(w, h int) []byte {
	p := make([]byte, 10)
	p[0] = 0x2f
	binary.LittleEndian.PutUint32(p[1:5], uint32(w-1)|uint32(h-1)<<14)
	return webpChunk("VP8L", p)
}

func webpLossless(w, h int) []byte { return webpFile(webpLosslessChunk(w, h)) }

func webpExtendedWith(w, h int, chunks ...[]byte) []byte {
	p := make([]byte, 10)
	put24 := func(at, v int) { p[at], p[at+1], p[at+2] = byte(v), byte(v>>8), byte(v>>16) }
	put24(4, w-1)
	put24(7, h-1)
	return webpFile(append([][]byte{webpChunk("VP8X", p)}, chunks...)...)
}

func webpExtended(w, h int) []byte { return webpExtendedWith(w, h, webpLosslessChunk(w, h)) }

func webpLossy(w, h int) []byte {
	p := []byte{0, 0, 0, 0x9d, 0x01, 0x2a, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(p[6:8], uint16(w))
	binary.LittleEndian.PutUint16(p[8:10], uint16(h))
	return webpFile(webpChunk("VP8 ", p))
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
		"webp alpha":    uri("image/webp", webpExtendedWith(128, 128, webpChunk("ALPH", make([]byte, 6)), webpLosslessChunk(128, 128))),
		"webp animated": uri("image/webp", webpExtendedWith(128, 128, webpChunk("ANIM", make([]byte, 6)), webpChunk("ANMF", make([]byte, 20)))),
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

// heard is a reader callback that reports each call on a channel (the callbacks run off the request).
func heard() (fn func(), calls chan struct{}) {
	calls = make(chan struct{}, 16)
	return func() { calls <- struct{}{} }, calls
}

func awaitCall(t *testing.T, calls chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader was not called")
	}
}

func noCall(t *testing.T, calls chan struct{}, why string) {
	t.Helper()
	select {
	case <-calls:
		t.Fatal(why)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPersonas_PutAnnouncesAnEventAndNotifiesReaders(t *testing.T) {
	m := personasModule(t)
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)
	fn, calls := heard()
	cancel := m.OnPersonasChange(fn)
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro")}, 0).Code)
	awaitCall(t, calls)

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
	noCall(t, calls, "a refused or stale PUT told the readers")
	cancel()
	require.Equal(t, http.StatusOK, putPersonas(m, []any{persona("hiro"), persona("anna")}, 1).Code)
	noCall(t, calls, "a cancelled reader heard a change")
}

// The readers are told after the answer is decided and off the request: one that blocks does not hold the PUT, one that
// panics does not take the daemon (or the other readers) down.
// Mutation gate: call the readers inline -> the blocking reader hangs the PUT -> red; drop the recover -> red.
func TestPersonas_AReaderThatBlocksOrPanicsDoesNotHurtThePut(t *testing.T) {
	m := personasModule(t)
	release := make(chan struct{})
	m.OnPersonasChange(func() { <-release })
	m.OnPersonasChange(func() { panic("a reader that fails") })
	fn, calls := heard()
	m.OnPersonasChange(fn)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- putPersonas(m, []any{persona("hiro")}, 0) }()
	select {
	case rr := <-done:
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	case <-time.After(2 * time.Second):
		t.Fatal("the PUT waited for a reader")
	}
	awaitCall(t, calls) // the reader after the panicking one still heard it
	close(release)
}

// A stored value that does not read is never taken for an empty set: the codes in it are reserved for good, so the GET, a
// 409 and a PUT all refuse and the row is left as it is (NP-1a R2).
// Mutation gate: let the PUT callback ignore the read error -> the PUT overwrites -> red.
func TestPersonas_AnUnreadableStoredValueIsNeverTakenForEmpty(t *testing.T) {
	m := personasModule(t)
	_, ok, err := m.store.Put(KeyPersonas, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(`{"items":"not a list"}`), nil })
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, http.StatusInternalServerError, serve(m, http.MethodGet, "/api/hostconfig/personas", "").Code)
	assert.Equal(t, http.StatusInternalServerError, putPersonas(m, []any{persona("hiro")}, 1).Code, "a PUT on the current revision")
	assert.Equal(t, http.StatusInternalServerError, putPersonas(m, []any{persona("hiro")}, 0).Code, "a stale PUT, whose 409 would show the value")
	e, err := m.store.Get(KeyPersonas)
	require.NoError(t, err)
	assert.JSONEq(t, `{"items":"not a list"}`, string(e.Value))
	assert.EqualValues(t, 1, e.Revision)
}

// A WebP is checked past its first bytes: the RIFF size, the first chunk's size and (for the extended layout) the picture
// chunk after it must agree with the file.
// Mutation gate: drop any one of these checks -> that picture is accepted -> red.
func TestPersonas_ABrokenWebPIsRefused(t *testing.T) {
	good := webpLossless(64, 64)
	badRiff := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(badRiff[4:8], uint32(len(good))) // one too many
	bigChunk := webpFile(append([]byte("VP8L"), 0xff, 0xff, 0, 0, 0x2f, 0, 0, 0, 0))
	noSignature := webpFile(webpChunk("VP8L", make([]byte, 10)))
	extAlone := webpFile(webpChunk("VP8X", make([]byte, 10)))
	extBadNext := webpFile(webpChunk("VP8X", make([]byte, 10)), webpChunk("JUNK", make([]byte, 10)))
	smallLossy := webpFile(webpChunk("VP8 ", []byte{0, 0, 0, 0x9d, 0x01, 0x2a}))
	ext := webpChunk("VP8X", make([]byte, 10))
	extAlphaOnly := webpFile(ext, webpChunk("ALPH", make([]byte, 6)))
	extAnimOnly := webpFile(ext, webpChunk("ANIM", make([]byte, 6)))
	extAnimNoFrame := webpFile(ext, webpChunk("ANIM", make([]byte, 6)), webpChunk("EXIF", make([]byte, 4)))
	extCutFrame := webpFile(ext, webpChunk("ANIM", make([]byte, 6)), webpChunk("ANMF", make([]byte, 4)))
	extAlphaThenJunk := webpFile(ext, webpChunk("ALPH", make([]byte, 6)), webpChunk("JUNK", make([]byte, 6)))
	cases := map[string][]byte{
		"truncated":                                                good[:len(good)-3],
		"the RIFF size disagrees":                                  badRiff,
		"a chunk bigger than a file":                               bigChunk,
		"no lossless signature":                                    noSignature,
		"an extended header alone":                                 extAlone,
		"an extended header, no picture chunk":                     extBadNext,
		"a lossy chunk too small for its header":                   smallLossy,
		"an extended header with alpha and no picture":             extAlphaOnly,
		"an extended header with an animation header and no frame": extAnimOnly,
		"an animation header followed by something else":           extAnimNoFrame,
		"an animation frame too small for its header":              extCutFrame,
		"alpha followed by an unknown chunk":                       extAlphaThenJunk,
		"trailing bytes":                                           append(append([]byte(nil), good...), 1, 2, 3, 4),
		"not RIFF":                                                 append([]byte("RIFX"), good[4:]...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			m := personasModule(t)
			p := persona("hiro")
			p["avatar_data"] = uri("image/webp", data)
			rr := putPersonas(m, []any{p}, 0)
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			assert.Contains(t, rr.Body.String(), "avatar_data")
		})
	}
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
