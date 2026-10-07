//go:build darwin

package modevents

import (
	"net"

	"golang.org/x/sys/unix"
)

// connPeerUID reads the peer's effective uid with LOCAL_PEERCRED.
func connPeerUID(c net.Conn) (uint32, error) {
	var uid uint32
	err := rawFD(c, func(fd int) error {
		cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			return err
		}
		uid = cred.Uid
		return nil
	})
	return uid, err
}
