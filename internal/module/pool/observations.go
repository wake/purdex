package poolmod

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// handleObservations is a host's observations since a time (wwd spec 8.4), asked of the local wwd with that host: the stored records and the
// live ones, oldest first, at most the newest 1000 with `truncated` and `next_until` when more existed. The answer is wwd's, as it wrote it:
// next_until goes back to wwd as the App got it (until=<next_until>, inclusive, so a record can be on both pages: drop repeats by id), and is
// not re-formatted here. A host whose wwd predates the route is "not known" (known: false) with no observations.
func (m *Module) handleObservations(w http.ResponseWriter, r *http.Request) {
	alias, since, until, ok := observationQuery(w, r, true)
	if !ok {
		return
	}
	m.serve(w, r, "observations|"+alias+"|"+since+"|"+until, func(ctx context.Context) (any, *apiError) {
		raw, known, err := m.client().Observations(ctx, alias, since, until)
		if err != nil {
			return nil, historyError(err)
		}
		if !known {
			return map[string]any{"host": alias, "known": false, "observations": []any{}}, nil
		}
		return json.RawMessage(raw), nil
	})
}

// handleUndeclared is the same records grouped by command signature (the biggest CPU first), for a person to decide which commands need a
// manifest or a hook rule. Everything else is as for handleObservations.
func (m *Module) handleUndeclared(w http.ResponseWriter, r *http.Request) {
	alias, since, _, ok := observationQuery(w, r, false)
	if !ok {
		return
	}
	m.serve(w, r, "undeclared|"+alias+"|"+since, func(ctx context.Context) (any, *apiError) {
		raw, known, err := m.client().Undeclared(ctx, alias, since)
		if err != nil {
			return nil, historyError(err)
		}
		if !known {
			return map[string]any{"host": alias, "known": false, "groups": []any{}}, nil
		}
		return json.RawMessage(raw), nil
	})
}

// observationQuery checks the host and the times of an observation read and answers 400 for a bad one, so that wwd is not asked. since and
// until are RFC 3339 (spec 8.4); they are passed on as given, never re-formatted: until is a cursor that wwd gave with a fraction of a
// second, and cutting it would skip records. wantUntil is false for the grouped read, which has no until.
func observationQuery(w http.ResponseWriter, r *http.Request, wantUntil bool) (alias, since, until string, ok bool) {
	alias = r.PathValue("alias")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, a dot name (. or ..), too long or holds a control character")
		return "", "", "", false
	}
	q := r.URL.Query()
	since = q.Get("since")
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadRequest, "since must be RFC 3339")
			return "", "", "", false
		}
	}
	if wantUntil {
		until = q.Get("until")
		if until != "" {
			if _, err := time.Parse(time.RFC3339, until); err != nil {
				writeErr(w, http.StatusBadRequest, ErrBadRequest, "until must be RFC 3339")
				return "", "", "", false
			}
		}
	}
	return alias, since, until, true
}
