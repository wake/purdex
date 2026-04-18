package main

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// listenWithReuseAddr opens a TCP listener with SO_REUSEADDR set so the daemon
// can re-bind immediately after exec-self during dev rebuild. On EADDRINUSE
// (TIME_WAIT race), retries up to 5 times with exponential backoff (200ms → 1s).
func listenWithReuseAddr(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var opErr error
			if err := c.Control(func(fd uintptr) {
				opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return opErr
		},
	}

	backoff := 200 * time.Millisecond
	var lastErr error
	for i := 0; i < 5; i++ {
		l, err := lc.Listen(context.Background(), "tcp", addr)
		if err == nil {
			return l, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, err
		}
		time.Sleep(backoff)
		if backoff < time.Second {
			backoff *= 2
			if backoff > time.Second {
				backoff = time.Second
			}
		}
	}
	return nil, lastErr
}
