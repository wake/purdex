// cmd/tbox/quick.go
package main

import (
	"fmt"
	"net"
	"os"
	"strings"
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
