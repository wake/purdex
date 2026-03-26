package dev

import (
	"encoding/json"
	"net/http"
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
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
