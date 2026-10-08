//go:build !darwin

package resources

import "context"

func osSysctlRaw(string) ([]byte, error) { return nil, ErrUnsupported }
func osSysctlU32(string) (uint32, error) { return 0, ErrUnsupported }
func osSysctlU64(string) (uint64, error) { return 0, ErrUnsupported }

type unsupportedSampler struct{}

func (unsupportedSampler) Sample(context.Context) (HostRaw, []Proc, error) {
	return HostRaw{}, nil, ErrUnsupported
}

// NewSampler returns a sampler that always reports ErrUnsupported.
func NewSampler() Sampler { return unsupportedSampler{} }
