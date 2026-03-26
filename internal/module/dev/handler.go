package dev

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type UpdateCheckResponse struct {
	Version      string `json:"version"`
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
}

func (m *DevModule) handleCheck(w http.ResponseWriter, r *http.Request) {
	resp := UpdateCheckResponse{
		Version:      m.readVersion(),
		SPAHash:      m.hashFn("spa/"),
		ElectronHash: m.hashFn("electron/", "electron.vite.config.ts"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *DevModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	outDir := filepath.Join(m.repoRoot, "out")
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		http.Error(w, "out/ directory not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"out.tar.gz\"")

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
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

		// Top-level entries: only allow main/ and preload/ directories
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		topLevel := parts[0]
		if topLevel != "main" && topLevel != "preload" {
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

	if err != nil {
		// Headers already sent; nothing we can do about the status code
		return
	}
}
