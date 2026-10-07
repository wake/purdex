//go:build linux

package modevents

import (
	"net"

	"golang.org/x/sys/unix"
)

// connPeerUID reads the peer's uid with SO_PEERCRED.
func connPeerUID(c net.Conn) (uint32, error) {
	var uid uint32
	err := rawFD(c, func(fd int) error {
		cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			return err
		}
		uid = cred.Uid
		return nil
	})
	return uid, err
}
