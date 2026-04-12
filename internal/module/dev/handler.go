package dev

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
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
	Version      string       `json:"version"`
	SPAHash      string       `json:"spaHash"`
	ElectronHash string       `json:"electronHash"`
	Source       SourceHashes `json:"source"`
	Building     bool         `json:"building"`
	BuildError   string       `json:"buildError"`
}

type BuildInfo struct {
	Version      string `json:"version"`
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
	BuiltAt      string `json:"builtAt"`
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

func (m *DevModule) handleCheck(w http.ResponseWriter, r *http.Request) {
	build := m.readBuildInfo()
	spaSource := m.hashFn("spa/")
	electronSource := m.hashFn("electron/", "electron.vite.config.ts")

	// Determine if source differs from build
	sourceChanged := build.SPAHash != spaSource || build.ElectronHash != electronSource

	m.mu.Lock()
	failedSameSource := m.buildError != "" && m.lastFailedSPA == spaSource && m.lastFailedElectron == electronSource
	if sourceChanged && !m.building && !failedSameSource {
		m.building = true
		m.buildError = ""
		m.lastFailedSPA = spaSource
		m.lastFailedElectron = electronSource
		go m.runBuild()
	}
	building := m.building
	buildError := m.buildError
	m.mu.Unlock()

	resp := UpdateCheckResponse{
		Version:      m.readVersion(),
		SPAHash:      build.SPAHash,
		ElectronHash: build.ElectronHash,
		Source: SourceHashes{
			SPAHash:      spaSource,
			ElectronHash: electronSource,
		},
		Building:   building,
		BuildError: buildError,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
