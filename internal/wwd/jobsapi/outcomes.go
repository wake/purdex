package jobsapi

import (
	"net/http"
	"strconv"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Disagreements is GET /v1/outcomes/disagreements?subject=<alias>&after=<id>&limit=<n> (spec §7): the disagreements the origin recorded
// that concern the subject host, those after the cursor, oldest first. It is how the owner of a label learns that it is to quarantine it:
// the host reads this from the origins it reads, so no host writes to another. Read-only, on both surfaces (the socket for the host's own
// reader, TCP with the pool token for a peer). Any host may ask about any host: the labels of every host are public in the pool, and this
// tells no more than the pool does. The parameter is not called host: ?host= is the socket's way of naming another host to forward a call
// to, and the TCP surface refuses it.
//
// The query is strict: subject is required and must be an alias, after is a non-negative integer (default 0), limit is 1 to
// wire.MaxDisagreements (default the maximum); anything else, or a parameter that is not one of these, is a 400.
func (a *API) Disagreements() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for k := range q {
			if k != "subject" && k != "after" && k != "limit" {
				badRequest(w, "unknown parameter "+strconv.Quote(k))
				return
			}
		}
		subject := q.Get("subject")
		if len(q["subject"]) != 1 || !wire.ValidAlias(subject) {
			badRequest(w, "subject must be given once, and be a host alias")
			return
		}
		// an optional parameter that is present has exactly one value and it is not empty: "after=" and "after=&after=1" are not "after
		// not given" (a proxy and this handler would read them differently)
		after := int64(0)
		if q.Has("after") {
			n, err := strconv.ParseInt(q.Get("after"), 10, 64)
			if err != nil || n < 0 || len(q["after"]) != 1 {
				badRequest(w, "after must be given once, as a non-negative integer")
				return
			}
			after = n
		}
		limit := wire.MaxDisagreements
		if q.Has("limit") {
			n, err := strconv.Atoi(q.Get("limit"))
			if err != nil || n < 1 || n > wire.MaxDisagreements || len(q["limit"]) != 1 {
				badRequest(w, "limit must be given once, between 1 and "+strconv.Itoa(wire.MaxDisagreements))
				return
			}
			limit = n
		}
		items, more, err := a.q.Disagreements(r.Context(), subject, after, limit)
		if err != nil {
			fail(w, err)
			return
		}
		cursor := after
		if n := len(items); n > 0 {
			cursor = items[n-1].ID
		}
		writeJSON(w, http.StatusOK, wire.DisagreementList{Host: subject, Cursor: cursor, Items: items, Origin: a.q.Origin(), More: more})
	}
}
