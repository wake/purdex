// cmd/pdx/quick.go
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
// Called before modules init and HTTP server start.
func initPairing(c *core.Core, cfg *config.Config, cfgPath string, quick bool) {
	if cfg.Token != "" {
		return
	}

	if quick {
		// Quick mode: interactive IP selection if needed
		if cfg.Bind == "" || cfg.Bind == "127.0.0.1" || cfg.Bind == "0.0.0.0" {
			cfg.Bind = selectBindIP()
			c.CfgMu.Lock()
			c.Cfg.Bind = cfg.Bind
			c.CfgMu.Unlock()
			if err := config.WriteFile(cfgPath, *cfg); err != nil {
				log.Printf("save bind config: %v", err)
			}
		}
		// Generate pairing secret
		secret := make([]byte, 3)
		if _, err := rand.Read(secret); err != nil {
			log.Fatalf("generate pairing secret: %v", err)
		}
		c.CfgMu.Lock()
		c.PairingSecret = hex.EncodeToString(secret)
		c.CfgMu.Unlock()
		c.Pairing.Set(core.StatePairing)

		ip := net.ParseIP(cfg.Bind).To4()
		code := pairing.EncodePairingCode(ip, uint16(cfg.Port), secret)
		fmt.Printf("\n配對碼: %s\n\n", code)
	} else {
		// General mode: generate token and persist to config file
		tokenBytes := make([]byte, 20)
		if _, err := rand.Read(tokenBytes); err != nil {
			log.Fatalf("generate token: %v", err)
		}
		token := "purdex_" + hex.EncodeToString(tokenBytes)
		cfg.Token = token
		c.CfgMu.Lock()
		c.Cfg.Token = token
		c.CfgMu.Unlock()
		if err := config.WriteFile(cfgPath, *cfg); err != nil {
			log.Printf("persist generated token: %v", err)
		}
		c.Pairing.Set(core.StatePending)

		fmt.Printf("\nToken: %s\n\n", token)
	}
}
