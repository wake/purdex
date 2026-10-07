//go:build !darwin && !linux

package modevents

import (
	"errors"
	"net"
)

// connPeerUID cannot read peer credentials here, so every connection is
// refused: the channel fails closed.
func connPeerUID(net.Conn) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on this platform")
}
