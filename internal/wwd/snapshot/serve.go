package snapshot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The origin's side of GET /v1/jobs/{id}/snapshot?claim_id=…&format=bundle&have=<sha> | format=tar (WWD spec §4.3): the snapshot commit
// of a job as a git bundle of have..commit, or as a tar of the commit (git archive). It is served for a live claim only.

// ErrStaleClaim is what a Source answers for a claim_id that is not the live claim of the job: unknown, expired, or given to another
// worker. The handler answers it 409 and changes nothing.
var ErrStaleClaim = errors.New("snapshot: the claim is not live")

// Ref says what a live claim may fetch. RepoDir is the repository on this host that holds the commit (the queue's private record: the
// wire job has no path); Base is the merge-base the snapshot was made against; State is the job's state, for the body of a 409.
type Ref struct {
	RepoDir string
	Commit  string
	Base    string
	State   wire.JobState
}

// Source is the queue's side (WWD-3b): in one transaction it compares the claim_id with the job's live claim. A claim that is not live
// is ErrStaleClaim, with Ref.State set when the job is known.
type Source interface {
	Claimed(ctx context.Context, jobID, claimID string) (Ref, error)
}

// maxBundle is the most a bundle may be: git is stopped when its output goes over it. A purdex snapshot against a stale base is a few
// MB (M-S1: 2.3 KB per commit the base is behind); a bigger one means an old or unrelated `have`, and the worker is better off with a
// tar. (A variable so that a test can lower it.)
var maxBundle = 256 << 20

// Handler serves the snapshot route. It does not check the pool token — the layer that mounts it does — only the claim.
type Handler struct{ src Source }

// NewHandler mounts as mux.Handle("GET /v1/jobs/{id}/snapshot", h): the job id is the path value "id".
func NewHandler(src Source) *Handler { return &Handler{src: src} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, q := r.PathValue("id"), r.URL.Query()
	claimID, format, have := q.Get("claim_id"), q.Get("format"), q.Get("have")
	switch {
	case validJobID(id) != nil:
		http.Error(w, "bad job id", http.StatusBadRequest)
		return
	case !wire.ValidClaimID(claimID):
		http.Error(w, "bad claim_id", http.StatusBadRequest)
		return
	case format != "bundle" && format != "tar":
		http.Error(w, "format must be bundle or tar", http.StatusBadRequest)
		return
	case format == "bundle" && !commitRe.MatchString(have):
		http.Error(w, "a bundle needs have=<commit>", http.StatusBadRequest)
		return
	}
	ref, err := h.src.Claimed(r.Context(), id, claimID)
	if errors.Is(err, ErrStaleClaim) {
		state := ref.State
		if !state.Valid() {
			state = wire.StateQueued
		}
		writeJSON(w, http.StatusConflict, wire.Conflict409{State: state})
		return
	}
	if err != nil || ref.RepoDir == "" || !commitRe.MatchString(ref.Commit) {
		http.Error(w, "snapshot: cannot look up the claim", http.StatusInternalServerError)
		return
	}
	// The queue pinned the commit when the job was submitted (the commit must stay reachable, and a bundle needs a ref name). This only
	// checks: a Pin here could bring back a ref the queue has just removed for a claim that ended between the lookup and now.
	if pinned(r.Context(), ref.RepoDir, id) != ref.Commit {
		http.Error(w, "snapshot: the commit of this job is not pinned", http.StatusInternalServerError)
		return
	}
	if format == "tar" {
		serveTar(w, r, ref)
		return
	}
	if !hasCommit(r.Context(), ref.RepoDir, have) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "have_unknown"}) // the worker asks again with an older base, or for a tar
		return
	}
	out, err := gitLimited(r.Context(), "wwd.snapshot-bundle", ref.RepoDir, maxBundle, "bundle", "create", "-", PinRef(id), "^"+have)
	switch {
	case errors.Is(err, errTooLarge):
		http.Error(w, "snapshot: the bundle is too large; ask for a tar", http.StatusRequestEntityTooLarge)
	case err != nil && strings.Contains(err.Error(), "empty bundle"):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "nothing_to_send"})
	case err != nil:
		http.Error(w, "snapshot: "+err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/x-git-bundle")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(out)
	}
}

// serveTar streams git archive. The first block is read before the answer is begun, so a git that fails at once is a 500; one that
// fails later aborts the connection, which the worker's tar reader sees as a cut archive.
func serveTar(w http.ResponseWriter, r *http.Request, ref Ref) {
	cmd := execstat.CommandContext(r.Context(), "wwd.snapshot-archive", "git", "-C", ref.RepoDir, "archive", "--format=tar", ref.Commit)
	cmd.Env = gitEnv()
	var errb strings.Builder
	cmd.Stderr = &errb
	pipe, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		http.Error(w, "snapshot: "+err.Error(), http.StatusInternalServerError)
		return
	}
	br := bufio.NewReaderSize(pipe, 64<<10)
	if _, perr := br.Peek(512); perr != nil { // a tar from git archive is padded to 10 KB at the least: less than a block means git failed
		_ = cmd.Wait()
		http.Error(w, fmt.Sprintf("snapshot: git archive: %s", strings.TrimSpace(errb.String())), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Cache-Control", "no-store")
	if _, cerr := io.Copy(w, br); cerr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return // the client went
	}
	if werr := cmd.Wait(); werr != nil {
		panic(http.ErrAbortHandler)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
