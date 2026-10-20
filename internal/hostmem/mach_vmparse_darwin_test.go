//go:build darwin

package hostmem

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vmBuf is a vm_statistics64 laid out as xnu does (osfmk/mach/vm_statistics.h), with a known value at each offset the reading uses
// and a different, wrong-looking one in the words around them so an off-by-one offset cannot pass.
func vmBuf(words int) []byte {
	b := make([]byte, words*4)
	for i := range b {
		b[i] = 0xEE
	}
	put32 := func(off int, v uint32) { binary.NativeEndian.PutUint32(b[off:], v) }
	put64 := func(off int, v uint64) { binary.NativeEndian.PutUint64(b[off:], v) }
	put32(0, 11)     // free_count
	put32(4, 22)     // active_count
	put32(8, 33)     // inactive_count
	put32(12, 44)    // wire_count
	put64(40, 55)    // pageouts
	put32(92, 66)    // speculative_count
	put64(96, 77)    // decompressions
	put32(132, 8888) // throttled_count (the word before external_page_count)
	put32(136, 99)   // external_page_count
	put32(140, 7777) // internal_page_count (the word after)
	return b
}

func TestParseVMCountersSynthetic(t *testing.T) {
	got, err := parseVMCounters(vmBuf(vmStatsWords))
	require.NoError(t, err)
	assert.Equal(t, VMCounters{Free: 11, Active: 22, Inactive: 33, Wired: 44, Pageouts: 55, Speculative: 66, Decompressions: 77, External: 99}, got)
}

// A reply that stops before external_page_count (offset 136, four bytes: 140 in all) is refused, not read past its end.
func TestParseVMCountersLengthCheck(t *testing.T) {
	_, err := parseVMCounters(vmBuf(vmStatsWords)[:139]) // one byte short of external_page_count
	assert.Error(t, err)
	_, err = parseVMCounters(vmBuf(vmStatsWords)[:140]) // exactly 140 bytes
	assert.NoError(t, err)
}

// The breakdown is a sane share of the host's memory: no part is more than all the pages the host has. (Not compared with vm_stat's
// numbers one by one: they are not read at the same instant.)
func TestMach_VMBreakdownIsSane(t *testing.T) {
	m, err := NewMach()
	require.NoError(t, err)
	vm, err := m.VM()
	require.NoError(t, err)
	const pages = (uint64(1) << 40) / 4096 // 1 TiB of 4 KiB pages: more than any Mac has, so no part may pass it
	for name, v := range map[string]uint64{"active": vm.Active, "wired": vm.Wired, "inactive": vm.Inactive, "external": vm.External} {
		assert.Positive(t, v, name)
		assert.Less(t, v, pages, name)
	}
}
