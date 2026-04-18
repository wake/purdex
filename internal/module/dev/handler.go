package dev

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type SourceHashes struct {
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
}

type UpdateCheckResponse struct {
	Version             string       `json:"version"`
	SPAHash             string       `json:"spaHash"`
	ElectronHash        string       `json:"electronHash"`
	Source              SourceHashes `json:"source"`
	Building            bool         `json:"building"`
	BuildError          string       `json:"buildError"`
	RequiresFullRebuild bool         `json:"requiresFullRebuild"`
	FullRebuildReason   string       `json:"fullRebuildReason,omitempty"`
}

type BuildInfo struct {
	Version      string `json:"version"`
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
	RebuildHash  string `json:"rebuildHash,omitempty"`
	BuiltAt      string `json:"builtAt"`
}

// streamEvent is the SSE payload for /check/stream. It carries either a
// build event (phase/stdout/stderr/done/error) or, for the initial and
// terminal frames, a check snapshot.
type streamEvent struct {
	Type  string               `json:"type"`
	Phase string               `json:"phase,omitempty"`
	Line  string               `json:"line,omitempty"`
	Error string               `json:"error,omitempty"`
	Check *UpdateCheckResponse `json:"check,omitempty"`
}

func (m *DevModule) readBuildInfo() BuildInfo {
	path := filepath.Join(m.repoRoot, "out", ".build-info.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildInfo{SPAHash: "unknown", ElectronHash: "unknown"}
	}
	var info BuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return BuildInfo{SPAHash: "unknown", ElectronHash: "unknown"}
	}
	return info
}

// snapshotCheck evaluates the current state and, if source is stale and no
// build is in flight (or previously failed on the same source), kicks one
// off. It returns the response payload plus the in-flight session (or nil
// if no build is running). Must not be called with m.mu held.
func (m *DevModule) snapshotCheck() (UpdateCheckResponse, *BuildSession) {
	build := m.readBuildInfo()
	spaSource := m.hashFn("spa/")
	electronSource := m.hashFn("electron/", "electron.vite.config.ts")
	sourceChanged := build.SPAHash != spaSource || build.ElectronHash != electronSource

	requiresFull, fullReason := m.detectRequiresFullRebuild(build.RebuildHash)

	m.mu.Lock()
	failedSameSource := m.buildError != "" && m.lastFailedSPA == spaSource && m.lastFailedElectron == electronSource
	var session *BuildSession
	if sourceChanged && !m.building && !failedSameSource {
		session = m.startBuildLocked(spaSource, electronSource)
	} else if m.building {
		session = m.buildSession
	}
	building := m.building
	buildError := m.buildError
	m.mu.Unlock()

	return UpdateCheckResponse{
		Version:             m.readVersion(),
		SPAHash:             build.SPAHash,
		ElectronHash:        build.ElectronHash,
		Source:              SourceHashes{SPAHash: spaSource, ElectronHash: electronSource},
		Building:            building,
		BuildError:          buildError,
		RequiresFullRebuild: requiresFull,
		FullRebuildReason:   fullReason,
	}, session
}

// observeCheck returns the current check snapshot without mutating state.
// Unlike snapshotCheck it never kicks off a build — callers that just want
// to report post-build results (e.g. the SSE terminal event) should use
// this to avoid retriggering a second build if .build-info.json was not
// written for any reason. Must not be called with m.mu held.
func (m *DevModule) observeCheck() UpdateCheckResponse {
	build := m.readBuildInfo()
	spaSource := m.hashFn("spa/")
	electronSource := m.hashFn("electron/", "electron.vite.config.ts")
	requiresFull, fullReason := m.detectRequiresFullRebuild(build.RebuildHash)

	m.mu.Lock()
	building := m.building
	buildError := m.buildError
	m.mu.Unlock()

	return UpdateCheckResponse{
		Version:             m.readVersion(),
		SPAHash:             build.SPAHash,
		ElectronHash:        build.ElectronHash,
		Source:              SourceHashes{SPAHash: spaSource, ElectronHash: electronSource},
		Building:            building,
		BuildError:          buildError,
		RequiresFullRebuild: requiresFull,
		FullRebuildReason:   fullReason,
	}
}

func (m *DevModule) handleCheck(w http.ResponseWriter, r *http.Request) {
	resp, _ := m.snapshotCheck()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *DevModule) handleCheckStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeEvent := func(ev streamEvent) bool {
		data, err := json.Marshal(ev)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	initial, session := m.snapshotCheck()
	if !writeEvent(streamEvent{Type: "check", Check: &initial}) {
		return
	}

	// No build in flight → stream a terminal done and close.
	if session == nil {
		writeEvent(streamEvent{Type: "done", Check: &initial})
		return
	}

	ch, replay, unsub := session.subscribe()
	defer unsub()
	for _, ev := range replay {
		if isTerminalBuildEvent(ev) {
			continue
		}
		if !writeEvent(toStreamEvent(ev)) {
			return
		}
	}

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// Session finished; send a single terminal event carrying the
				// authoritative post-build check snapshot (includes buildError
				// if the build failed). Use observeCheck so a missing
				// .build-info.json post-build doesn't retrigger a second
				// build.
				final := m.observeCheck()
				writeEvent(streamEvent{Type: "done", Check: &final})
				return
			}
			if isTerminalBuildEvent(ev) {
				continue
			}
			if !writeEvent(toStreamEvent(ev)) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func isTerminalBuildEvent(ev BuildEvent) bool {
	return ev.Type == BuildEventDone || ev.Type == BuildEventError
}

func toStreamEvent(ev BuildEvent) streamEvent {
	return streamEvent{
		Type:  string(ev.Type),
		Phase: ev.Phase,
		Line:  ev.Line,
		Error: ev.Error,
	}
}

func (m *DevModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	building := m.building
	m.mu.Unlock()
	if building {
		http.Error(w, "build in progress", http.StatusConflict)
		return
	}

	outDir := filepath.Join(m.repoRoot, "out")
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		http.Error(w, "out/ directory not found", http.StatusNotFound)
		return
	}

	// Buffer the tar.gz in memory so we can return an error status if Walk fails
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	walkErr := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Compute path relative to outDir (e.g., "main/index.mjs")
		rel, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}

		// Skip root "."
		if rel == "." {
			return nil
		}

		// Top-level entries: only allow main/, preload/, and renderer/ directories
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		topLevel := parts[0]
		if topLevel != "main" && topLevel != "preload" && topLevel != "renderer" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories themselves (only include files)
		if info.IsDir() {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})

	tw.Close()
	gw.Close()

	if walkErr != nil {
		http.Error(w, "tar creation failed: "+walkErr.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"out.tar.gz\"")
	w.Write(buf.Bytes())
}
