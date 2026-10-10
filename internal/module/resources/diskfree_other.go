//go:build !unix

package resourcesmod

import "errors"

// statFreeBytes: not implemented off unix; the disk guard then stays quiet.
func statFreeBytes(string) (int64, error) {
	return 0, errors.New("free space is not read on this platform")
}
