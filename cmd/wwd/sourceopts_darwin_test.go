//go:build darwin

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wake/purdex/internal/wwd/facts"
)

// On a Mac the real sources made from these options read the owner, with `[owner] enabled` never consulted: this is what an air26 whose
// owner rule the user turned off, or an mlab that never had one, reports in facts.owner.
func TestARealMacReadsTheOwnerWithoutTheRule(t *testing.T) {
	src := facts.SystemSourcesWith(sourceOptions(nil, func() int { return 0 }))
	assert.NotNil(t, src.Owner, "facts.owner would be null: the monitoring page could not show whether the owner is at the machine")
}
