package peers

import (
	"context"
	"sort"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// PeerNameStore is the peer_names table (store.PeerNameStore): each
// conversation's pdx-assigned virtual name (Peer Address v5, peer mailbox
// spec §3.2).
type PeerNameStore interface {
	Assign(ctx context.Context, sessionID, ref, name, source string, nowMs int64) (store.PeerNameEntry, error)
	AdoptLineage(ctx context.Context, sessionID, name string) (store.PeerNameEntry, error)
	Lookup(ctx context.Context, sessionIDs []string) (map[string]store.PeerNameEntry, error)
	ByRefs(ctx context.Context, refs []string) (map[string]string, error)
}

// ConversationNameReader reads the registry names recorded for conversations
// (store.ConversationNameStore), the second base source.
type ConversationNameReader interface {
	All(ctx context.Context) (map[string]string, error)
}

// WithPeerNames wires the virtual-name store and the conversation-name reader
// it falls back on; a nil store (the default) assigns no names, and every
// address takes the ref form. Returns m for chaining.
func (m *Module) WithPeerNames(s PeerNameStore, convNames ConversationNameReader) *Module {
	m.peerNames, m.convNames = s, convNames
	return m
}

// nameCandidate is one conversation an inventory pass may name: its session
// id and ref, the registry name its live entry carries now, its relay
// lineage (newest first) and, for an execution only (P4), its cwd basename.
type nameCandidate struct {
	sid, ref, registryName string
	previousRefs           []string
	dirBase                string
}

// resolveNames answers the virtual name of every candidate, keyed by the
// session id as given, assigning one to a conversation seen for the first
// time (spec §3.2). Per conversation, in order:
//
//  1. its lineage name: the first of previousRefs (newest first) that has a
//     stored name;
//  2. its stored row: a lineage row is final; any other is upgraded to the
//     lineage name when there is one (AdoptLineage), else used as is;
//  3. no row: the lineage name, else a name from the live registry name, the
//     recorded conversation name, or (executions) the cwd basename, stored
//     by Assign.
//
// Race-free across concurrent passes: Assign inserts with DO NOTHING and
// answers the stored row, so two passes over one new conversation converge
// on whichever insert won. A pass that sees the lineage either inserts the
// lineage name first or, finding the other pass's fallback row, upgrades it
// through AdoptLineage — so the lineage name always wins in the end — and
// AdoptLineage's own `source <> 'lineage'` guard means no pass ever
// overwrites a lineage row, whatever stale row it read.
//
// A store error leaves that conversation without a name for this pass (its
// address takes the ref form) and is logged once per pass: a name is only
// ever one the store holds, never one invented here.
func (m *Module) resolveNames(ctx context.Context, cands []nameCandidate) map[string]string {
	out := map[string]string{}
	if m.peerNames == nil || len(cands) == 0 {
		return out
	}
	byKey, keys := mergeCandidates(cands)

	var failures int
	var firstErr error
	fail := func(err error) {
		if failures == 0 {
			firstErr = err
		}
		failures++
	}
	defer func() {
		if failures > 0 {
			m.logf("peers: virtual names: %d store error(s) this pass, those rows use their ref: %v", failures, firstErr)
		}
	}()

	rows, err := m.peerNames.Lookup(ctx, keys)
	if err != nil {
		fail(err)
		return out
	}
	var refs []string
	for _, k := range keys {
		refs = append(refs, byKey[k].previousRefs...)
	}
	var lineage map[string]string
	lineageErr := error(nil)
	if len(refs) > 0 {
		lineage, lineageErr = m.peerNames.ByRefs(ctx, refs)
	}
	var conv map[string]string
	convRead := false

	nowMs := m.now().UnixMilli()
	for _, k := range keys {
		c := byKey[k]
		if len(c.previousRefs) > 0 && lineageErr != nil {
			fail(lineageErr) // cannot tell whether it inherits a name
			continue
		}
		ln := ""
		for _, r := range c.previousRefs {
			if n := lineage[r]; n != "" {
				ln = n
				break
			}
		}
		row, has := rows[k]
		if !has {
			name, source := ln, store.PeerNameSourceLineage
			if name == "" {
				if !convRead && m.convNames != nil && !ipeers.RoutableName(c.registryName) {
					conv, err = m.convNames.All(ctx)
					if err != nil {
						conv = nil
						fail(err)
					}
					convRead = true
				}
				name, source = baseName(c, conv[k])
			}
			if name == "" {
				continue // nothing to name it after: the ref form
			}
			if row, err = m.peerNames.Assign(ctx, k, c.ref, name, source, nowMs); err != nil {
				fail(err)
				continue
			}
		}
		if row.Source != store.PeerNameSourceLineage && ln != "" {
			upgraded, err := m.peerNames.AdoptLineage(ctx, k, ln)
			if err != nil {
				fail(err) // the stored fallback still stands this pass
			} else {
				row = upgraded
			}
		}
		if ipeers.RoutableName(row.Name) {
			out[c.sid] = row.Name
		}
	}
	return out
}

// baseName picks a fallback name for a conversation without a row or a
// lineage name: the live registry name, else the recorded conversation name
// (conv), else the cwd basename. "" when no source makes a valid name.
func baseName(c nameCandidate, conv string) (name, source string) {
	if ipeers.RoutableName(c.registryName) {
		if n, ok := ipeers.VirtualName(c.registryName, c.ref); ok {
			return n, store.PeerNameSourceRegistry
		}
	}
	if ipeers.RoutableName(conv) {
		if n, ok := ipeers.VirtualName(conv, c.ref); ok {
			return n, store.PeerNameSourceConversationName
		}
	}
	if c.dirBase != "" {
		if n, ok := ipeers.VirtualName(ipeers.NormalizeBase(c.dirBase), c.ref); ok {
			return n, store.PeerNameSourceDir
		}
	}
	return "", ""
}

// mergeCandidates folds candidates by lowercase session id (the store's key)
// and returns them with the keys sorted. Two live entries of one
// conversation (a resume pair) carry one sid: the smallest routable registry
// name is kept, so the pick is deterministic.
func mergeCandidates(cands []nameCandidate) (map[string]nameCandidate, []string) {
	byKey := make(map[string]nameCandidate, len(cands))
	for _, c := range cands {
		k := strings.ToLower(c.sid)
		if k == "" || !ipeers.IsRef(c.ref) {
			continue
		}
		cur, seen := byKey[k]
		if !seen {
			byKey[k] = c
			continue
		}
		if ipeers.RoutableName(c.registryName) &&
			(!ipeers.RoutableName(cur.registryName) || c.registryName < cur.registryName) {
			cur.registryName = c.registryName
		}
		if len(cur.previousRefs) == 0 {
			cur.previousRefs = c.previousRefs
		}
		if cur.dirBase == "" {
			cur.dirBase = c.dirBase
		}
		byKey[k] = cur
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return byKey, keys
}
