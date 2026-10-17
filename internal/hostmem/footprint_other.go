//go:build !darwin

package hostmem

import "errors"

// Footprinter has nothing to read off macOS: the resident size is what a list of processes by memory uses there (#3057).
type Footprinter struct{}

// NewFootprinter always says the footprint is not to be had.
func NewFootprinter() (*Footprinter, error) {
	return nil, errors.New("hostmem: no physical footprint on this system")
}

// Of is never called: there is no Footprinter.
func (*Footprinter) Of(int) (uint64, error) {
	return 0, errors.New("hostmem: no physical footprint on this system")
}
