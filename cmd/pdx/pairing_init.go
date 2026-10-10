// cmd/pdx/pairing_init.go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/pairing"
)

type ifaceEntry struct {
	Name string
	IP   net.IP
}

// listNonLoopbackIPs returns non-loopback, non-link-local IPv4 addresses.
func listNonLoopbackIPs() ([]ifaceEntry, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var result []ifaceEntry
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.To4() == nil {
				continue
			}
			result = append(result, ifaceEntry{Name: iface.Name, IP: ip})
		}
	}
	return result, nil
}

// selectBindIP interactively selects a bind IP.
// Returns the selected IP string or exits on error.
func selectBindIP() string {
	entries, err := listNonLoopbackIPs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing interfaces: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "no non-loopback IPv4 addresses found")
		os.Exit(1)
	}
	if len(entries) == 1 {
		fmt.Printf("Using %s (%s)\n", entries[0].IP, entries[0].Name)
		return entries[0].IP.String()
	}

	fmt.Println("Select bind address:")
	for i, e := range entries {
		fmt.Printf("  %d) %s (%s)\n", i+1, e.IP, e.Name)
	}
	fmt.Print("Choice [1]: ")
	var input string
	fmt.Scanln(&input)
	input = strings.TrimSpace(input)
	if input == "" {
		input = "1"
	}
	var idx int
	fmt.Sscanf(input, "%d", &idx)
	if idx < 1 || idx > len(entries) {
		fmt.Fprintln(os.Stderr, "invalid choice")
		os.Exit(1)
	}
	selected := entries[idx-1]
	fmt.Printf("Using %s (%s)\n", selected.IP, selected.Name)
	return selected.IP.String()
}

// initPairing sets up the daemon's pairing mode based on config and flags.
// Called before modules init and HTTP server start. A daemon never serves with an empty token: it pairs (quick) or
// generates one (general). An error means it cannot — the caller must not start.
func initPairing(c *core.Core, cfg *config.Config, cfgPath string, quick bool) error {
	return initPairingTo(c, cfg, cfgPath, quick, os.Stdout, isTerminal(os.Stdout))
}

// isTerminal says whether f is a character device: a person's terminal, not a log file or a pipe.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// initPairingTo is initPairing writing its notices to out; tty says whether out is a terminal. A generated token is shown in
// plaintext only to a terminal (the person starting the daemon in the foreground, who needs it to pair an App); to a log or a pipe
// goes its fingerprint and where it is (#1950).
func initPairingTo(c *core.Core, cfg *config.Config, cfgPath string, quick bool, out io.Writer, tty bool) error {
	if cfg.Token != "" {
		return nil
	}

	if quick {
		// Quick mode: interactive IP selection if needed
		if cfg.Bind == "" || cfg.Bind == "127.0.0.1" || cfg.Bind == "0.0.0.0" {
			cfg.Bind = selectBindIP()
			c.CfgMu.Lock()
			c.Cfg.Bind = cfg.Bind
			c.CfgMu.Unlock()
			// Its own field, on top of the file: nothing else of the in-memory config (a --port override) is made permanent.
			bind := cfg.Bind
			if _, err := config.Update(cfgPath, func(f *config.Config) error { f.Bind = bind; return nil }); err != nil {
				log.Printf("save bind config: %v", err)
			}
		}
		// Generate pairing secret
		secret := make([]byte, 3)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("generate pairing secret: %w", err)
		}
		c.CfgMu.Lock()
		c.PairingSecret = hex.EncodeToString(secret)
		c.CfgMu.Unlock()
		c.Pairing.Set(core.StatePairing)

		ip := net.ParseIP(cfg.Bind).To4()
		code := pairing.EncodePairingCode(ip, uint16(cfg.Port), secret)
		fmt.Fprintf(out, "\n配對碼: %s\n\n", code)
		return nil
	}

	// General mode: generate a token and persist it to the config file.
	tokenBytes := make([]byte, 20)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	token := "purdex_" + hex.EncodeToString(tokenBytes)
	if err := persistGeneratedToken(cfgPath, token); err != nil {
		// Not served on a token no client can learn (the next start would make another): the runtime state is untouched.
		return fmt.Errorf("the daemon has no token and cannot write one to %s (%v); make that file writable, or give it a token yourself with `pdx token generate --config %s`", cfgPath, err, cfgPath)
	}
	cfg.Token = token
	c.CfgMu.Lock()
	c.Cfg.Token = token
	c.CfgMu.Unlock()
	c.Pairing.Set(core.StatePending)

	if tty {
		fmt.Fprintf(out, "\nToken: %s\n\n", token)
	} else {
		fmt.Fprintf(out, "\nToken generated (fingerprint %s); it is in %s (mode 0600)\n\n", config.TokenFingerprint(token), cfgPath)
	}
	return nil
}

// persistGeneratedToken writes token into the config file as the FILE has it, not the in-memory config (a --bind / --port
// override is not made permanent): config.SetToken edits that one line under a file lock, keeping every other byte.
func persistGeneratedToken(cfgPath, token string) error {
	return config.SetToken(cfgPath, token)
}
