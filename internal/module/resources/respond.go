package resourcesmod

import (
	"encoding/json"
	"net/http"

	"github.com/wake/purdex/internal/resources"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, resources.APIError{Error: code, Detail: detail})
}
