package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

func runSetup(args []string) {
	var agentType string
	remove := false
	force := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				agentType = args[i+1]
				i++
			}
		case "--remove":
			remove = true
		case "--force":
			force = true
		}
	}

	if agentType == "" {
		fmt.Fprintf(os.Stderr, "pdx setup: --agent flag is required (e.g. --agent cc, --agent codex, --agent opencode)\n")
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

	// The mod folder is rewritten for cc, which reloads the mod of every session: not over a relay under way (#2441).
	if agentType == "cc" {
		if code := setupRelayGuard(&http.Client{Timeout: 5 * time.Second}, baseURL, token, force, os.Stderr); code != ExitOK {
			os.Exit(code)
		}
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

	// Short timeout: daemon is local, responds instantly if running.
	// Fallback only triggers on transport errors (connection refused, timeout),
	// not on HTTP 4xx/5xx from a running daemon.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: daemon unreachable, installing hooks locally\n")
		if err := localSetup(agentType, remove); err != nil {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			os.Exit(1)
		}
		if remove {
			fmt.Printf("pdx hooks for %s removed\n", agentType)
		} else {
			fmt.Printf("pdx hooks for %s installed\n", agentType)
		}
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "setup: failed (%d): %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	if remove {
		fmt.Printf("pdx hooks for %s removed\n", agentType)
	} else {
		fmt.Printf("pdx hooks for %s installed\n", agentType)
	}
}

// localSetup installs or removes hooks directly without the daemon.
// The hook methods on CC/Codex providers don't use any injected dependencies,
// so we can construct providers with nil deps for local-only operation.
func localSetup(agentType string, remove bool) error {
	pdxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find pdx binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(pdxPath); err == nil {
		pdxPath = resolved
	}

	switch agentType {
	case "cc":
		p := agentcc.NewProvider(nil, nil, nil, nil)
		if remove {
			return p.RemoveHooks(pdxPath)
		}
		return p.InstallHooks(pdxPath)
	case "codex":
		p := codex.NewProvider()
		if remove {
			return p.RemoveHooks(pdxPath)
		}
		return p.InstallHooks(pdxPath)
	case "opencode":
		p := opencode.NewProvider()
		if remove {
			return p.RemoveHooks(pdxPath)
		}
		return p.InstallHooks(pdxPath)
	default:
		return fmt.Errorf("unknown agent type: %s (supported: cc, codex, opencode)", agentType)
	}
}

// setupRelayGuard is ExitRefused (and says why on stderr) while the daemon reports relays under way, unless force.
// It guards only what it can see: a daemon that does not answer, or answers without the list (an older one), is
// not a reason to refuse.
func setupRelayGuard(client *http.Client, baseURL, token string, force bool, stderr io.Writer) int {
	if force {
		return ExitOK
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/team/inflight", nil)
	if err != nil {
		return ExitOK
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ExitOK
	}
	defer resp.Body.Close()
	var inf team.InflightResponse
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&inf) != nil || inf.RelaysActive == 0 {
		return ExitOK
	}
	var b strings.Builder
	fmt.Fprintf(&b, "setup: %d relay(s) under way would lose their mod:\n", inf.RelaysActive)
	for _, r := range inf.Relays {
		fmt.Fprintf(&b, "  %s  %s %s  %s\n", r.ID, r.Kind, r.Ref, r.State)
	}
	b.WriteString("wait for them to finish, or run again with --force.\nrelay_active\n")
	fmt.Fprint(stderr, b.String())
	return ExitRefused
}
