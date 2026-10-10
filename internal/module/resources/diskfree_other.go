//go:build !darwin && !linux

package resourcesmod

import "errors"

// statFreeBytes: not implemented off darwin and linux (the `unix` tag also selects platforms whose syscall package has no
// Statfs); the disk guard then stays quiet.
func statFreeBytes(string) (int64, error) {
	return 0, errors.New("free space is not read on this platform")
}
