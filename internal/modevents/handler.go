package modevents

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// EventsPath is the channel's one route.
const EventsPath = "/mod/v1/events"

// MaxBody caps a request body; larger bodies get 413.
const MaxBody = 1 << 20

// NewHandler serves POST /mod/v1/events into reg: 200 {"ack":N}, 400
// {"error":"<code>"} (counted on the stream when its id was valid), 413
// {"error":"too_large"}, 503 {"error":"registry_full"} when Apply refuses
// a new stream with ErrRegistryFull (the mod backs off and resends), and
// 500 {"error":"internal"} for any other Apply error. Any other path is
// 404, any other method 405.
func NewHandler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EventsPath {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}
		b, err := DecodeBatch(http.MaxBytesReader(w, r.Body, MaxBody))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
				return
			}
			code := CodeBadJSON
			var we *WireError
			if errors.As(err, &we) {
				code = we.Code
				if we.Stream != "" {
					reg.Reject(we.Stream)
				}
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
			return
		}
		ack, err := reg.Apply(b)
		switch {
		case errors.Is(err, ErrRegistryFull):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "registry_full"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		default:
			writeJSON(w, http.StatusOK, map[string]int64{"ack": ack})
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v) // maps of strings and ints always marshal
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// NewServer is the channel's own server, with the timeouts of spec §6.1.
func NewServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
