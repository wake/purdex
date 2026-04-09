package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wake/tmux-box/internal/config"
)

func runSetup(args []string) {
	var agentType string
	remove := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				agentType = args[i+1]
				i++
			}
		case "--remove":
			remove = true
		}
	}

	if agentType == "" {
		fmt.Fprintf(os.Stderr, "tbox setup: --agent flag is required (e.g. --agent cc, --agent codex)\n")
		os.Exit(1)
	}

	cfg, err := config.Load("")
	var baseURL, token string
	if err != nil {
		baseURL = "http://127.0.0.1:7860"
	} else {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.Bind, cfg.Port)
		token = cfg.Token
	}

	action := "install"
	if remove {
		action = "remove"
	}

	body, _ := json.Marshal(map[string]string{"action": action})
	url := fmt.Sprintf("%s/api/hooks/%s/setup", baseURL, agentType)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: cannot reach daemon: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "setup: failed (%d): %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	if remove {
		fmt.Printf("tbox hooks for %s removed\n", agentType)
	} else {
		fmt.Printf("tbox hooks for %s installed\n", agentType)
	}
}
