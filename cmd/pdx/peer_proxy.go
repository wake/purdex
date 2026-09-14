// cmd/pdx/peer_proxy.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
)

// peerProxyProcStartFn is the `ps -o lstart=` oracle the helper records
// in its registry entry; tests replace it so nothing forks ps.
var peerProxyProcStartFn = ccuds.DefaultProcStart

// runPeerProxy is the `pdx peer-proxy` body: it impersonates one Claude
// Code peer on behalf of the daemon and speaks only over stdin/stdout. It
// ignores every argument, never reads config.toml and never opens HTTP.
// Exit 0 after a clean shutdown (stdin EOF or SIGTERM/SIGINT), 1 when
// the helper could not become ready.
func runPeerProxy() int {
	err := proxyhelper.Run(context.Background(), os.Stdin, os.Stdout, proxyhelper.Options{
		PID:       os.Getpid(),
		ProcStart: peerProxyProcStartFn,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "peer-proxy: %v\n", err)
		return 1
	}
	return 0
}
