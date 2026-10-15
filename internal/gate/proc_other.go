//go:build !darwin && !linux

package gate

import "errors"

func procTable() (map[int]procInfo, error) {
	return nil, errors.New("process table: unsupported platform")
}
