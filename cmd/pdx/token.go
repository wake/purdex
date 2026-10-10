package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/config"
)

func runToken(args []string) {
	// Extract subcommand ("generate") from args, pass the rest to flag parsing.
	// This allows both `pdx token generate --config X` and `pdx token --config X generate`.
	var subcmd string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "generate" {
			subcmd = args[i]
		} else {
			flagArgs = append(flagArgs, args[i])
		}
	}

	if subcmd != "generate" {
		fmt.Fprintf(os.Stderr, "Usage: pdx token generate [--config <path>]\n")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("token", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/pdx/config.toml)")
	fs.Parse(flagArgs)

	// Generate 20-byte random token → "pdx_" + 40 hex chars (160-bit entropy)
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		fmt.Fprintf(os.Stderr, "token: failed to generate random bytes: %v\n", err)
		os.Exit(1)
	}
	token := "pdx_" + hex.EncodeToString(raw)

	// Resolve config path
	path := *cfgPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "token: cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		path = filepath.Join(home, ".config", "pdx", "config.toml")
	}

	// One line of the file changes, under a lock (config.SetToken); every other setting, comment and table stays.
	if err := config.SetToken(path, token); err != nil {
		fmt.Fprintf(os.Stderr, "token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Token: %s\n", token)
	fmt.Printf("Saved to: %s\n", path)
}
