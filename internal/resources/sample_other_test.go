//go:build !darwin

package resources

import (
	"context"
	"errors"
	"testing"
)

func TestNewSampler_Unsupported(t *testing.T) {
	if _, _, err := NewSampler().Sample(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}
