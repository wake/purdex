package nex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

// rowBodyLimit caps one execution's response. A row is a few KiB (the
// brief is the largest field, and Nexen caps it); 1 MiB is far past any real
// row, so hitting it means something is wrong, not that a row is big.
const rowBodyLimit = 1 << 20

// executionIDPattern is what an id must look like before rowReader builds a
// request from it (§3.3). Nexen's ids are ASCII letters, digits, "_" and
// "-"; anything else — a "/" or ".." that would change the path, a "?" or
// "#", whitespace — is refused before a request exists.
var executionIDPattern = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)

// rowReader reads one execution through the engine's own HTTP handler, for
// the projector (spec 2026-10-08 §3.3; projector.go calls it inside readSlot.readThen,
// it never takes the slot itself). Going through the handler rather than
// the store means a delta row is exactly the JSON GET /v1/executions/{id}
// answers — the shape the SPA's getExecution already parses — including
// the fields the handler computes on top of the row (event_count,
// observers, the title refresh).
//
// The request is built here and only here: method GET, path
// "/v1/executions/<id>" (handler is the engine's own, which routes "/v1/..."
// — the module mounts it with RoutePrefix stripped), no headers (so
// principalAuth names the bare "pdx:<hostID>"; a GET needs no lease), no
// body, and the caller's context. Nothing from any user request ever
// reaches it. The projector's list walks read their pages through it too
// (page, projector_walk.go), built the same way.
type rowReader struct {
	handler http.Handler // the engine's handler (engine.handler), unprefixed
	logf    func(string, ...any)
}

// read answers id's row in the list's shape, found=false when Nexen says
// the execution does not exist (404 execution_not_found — a removal for the
// projector), or an error for anything else: an invalid id, any other
// status (any other 404 included: only Nexen's own code means "gone", a
// missing route must never read as a removal), a body that is not a JSON
// object, a body over rowBodyLimit, or a handler that panicked.
//
// Normalization (§3.3): "lease" and "live_turn_id" are deleted — the list
// never carries them, and the SPA store holds list-shaped rows only — and a
// missing "turn_count" is spelled out as 0, because the single GET omits it
// at zero (omitempty, kept for wire compatibility, N/api/query.go:119-123)
// while a list row always carries it. Every other value is passed through
// as the engine wrote it.
func (rr rowReader) read(ctx context.Context, id string) (row json.RawMessage, found bool, err error) {
	if !executionIDPattern.MatchString(id) {
		return nil, false, fmt.Errorf("nex row read: invalid execution id %q", id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/v1/executions/"+url.PathEscape(id), http.NoBody)
	if err != nil {
		return nil, false, fmt.Errorf("nex row read %s: %w", id, err)
	}

	res, panicked := rr.serve(req, rowBodyLimit)
	switch {
	case panicked:
		return nil, false, fmt.Errorf("nex row read %s: engine handler panicked", id)
	case res.overflow:
		return nil, false, fmt.Errorf("nex row read %s: response exceeds %d bytes", id, rowBodyLimit)
	}
	body := res.body.Bytes()
	switch code := res.code(); code {
	case http.StatusOK:
	case http.StatusNotFound:
		if errorCode(body) == "execution_not_found" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("nex row read %s: status 404 without execution_not_found: %s", id, snippet(body))
	default:
		return nil, false, fmt.Errorf("nex row read %s: status %d: %s", id, code, snippet(body))
	}

	obj, err := decodeObject(body)
	if err != nil {
		return nil, false, fmt.Errorf("nex row read %s: %w", id, err)
	}
	delete(obj, "lease")
	delete(obj, "live_turn_id")
	if _, ok := obj["turn_count"]; !ok {
		obj["turn_count"] = json.RawMessage("0")
	}
	out, err := encodeObject(obj)
	if err != nil {
		return nil, false, fmt.Errorf("nex row read %s: %w", id, err)
	}
	return bytes.TrimRight(out, "\n"), true, nil
}

// serve runs an in-process request through the engine's handler into a
// buffer capped at limit and reports whether the handler panicked. recoverer
// turns a panic into a 500 and a log line; the flag also catches a panic
// that came after a complete 200 had been written, which a status check
// alone would take for a good answer.
func (rr rowReader) serve(req *http.Request, limit int) (res *bufferedResponse, panicked bool) {
	res = newBufferedResponse(limit)
	h := recoverer(func(format string, args ...any) {
		panicked = true
		rr.logf(format, args...)
	}, rr.handler)
	func() {
		// recoverer re-panics http.ErrAbortHandler for net/http to handle,
		// and there is no net/http above an in-process read: stop it here
		// rather than let it take the daemon down.
		defer func() {
			if rec := recover(); rec != nil {
				panicked = true
			}
		}()
		h.ServeHTTP(res, req)
	}()
	return res, panicked
}

// errResponseTooLarge is what a bufferedResponse's Write returns once the
// body would pass its limit; the handler's encoder stops there.
var errResponseTooLarge = errors.New("response exceeds the buffer limit")

// bufferedResponse is an http.ResponseWriter that keeps a whole response in
// memory, so the module can inspect what the engine answered before any of
// it reaches a client (the list wrapper) or instead of reaching one at all
// (the row reader). The body is capped at limit bytes: past it, Write fails,
// overflow is set and nothing more is kept.
type bufferedResponse struct {
	header   http.Header
	status   int // 0 until WriteHeader or the first Write
	body     bytes.Buffer
	limit    int
	overflow bool
}

func newBufferedResponse(limit int) *bufferedResponse {
	return &bufferedResponse{header: make(http.Header), limit: limit}
}

func (b *bufferedResponse) Header() http.Header { return b.header }

// WriteHeader records the first status, as net/http does: a later call is
// superfluous and ignored.
func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.WriteHeader(http.StatusOK)
	if b.overflow || b.body.Len()+len(p) > b.limit {
		b.overflow = true
		return 0, errResponseTooLarge
	}
	return b.body.Write(p)
}

// code is the response's status: net/http's implicit 200 when the handler
// wrote nothing at all.
func (b *bufferedResponse) code() int {
	if b.status == 0 {
		return http.StatusOK
	}
	return b.status
}

// decodeObject decodes a JSON object's top level, keeping every value as
// the raw bytes it arrived as. Anything else — invalid JSON, an array, a
// scalar, or null (which Unmarshal would quietly decode into a nil map) —
// is an error.
func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("body is not a JSON object: %w", err)
	}
	if obj == nil {
		return nil, errors.New("body is not a JSON object: null")
	}
	return obj, nil
}

// encodeObject re-encodes a decoded top level. HTML escaping is off, so a
// raw value is only compacted, never rewritten: what Nexen wrote (already
// compact, already escaped by its own encoder) comes out byte for byte.
// The result ends in a newline, as Nexen's own bodies do.
func encodeObject(obj map[string]json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// errorCode reads the "code" of a Nexen error body ({"error", "code"}), or
// "" when the body is not one.
func errorCode(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Code
}

// snippet bounds how much of an unexpected body goes into an error.
func snippet(body []byte) string {
	const limit = 256
	if len(body) > limit {
		return string(body[:limit]) + "…"
	}
	return string(bytes.TrimRight(body, "\n"))
}
