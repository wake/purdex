//go:build darwin

package hostmem

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// residentOf reads the resident size from the same rusage buffer (ri_resident_size @64): the figure the memory list was ranked by before
// #3057, which is what the footprint is not.
func (f *Footprinter) residentOf(pid int) (uint64, error) {
	if _, err := f.Of(pid); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(f.buf[64:]), nil
}

// The footprint is not the resident size: clean pages of a file that is mapped read-only are resident but are nobody's footprint (macOS
// does not charge them to the process). Touching 256 MB of them grows the resident size by that much and the footprint by next to nothing,
// so the figure we return must not follow them. Mutation gate: the offset of the resident size (@64) read as the footprint -> the delta is
// 256 MB (red); measured on this macOS: footprint +1 MB, resident +257 MB.
func TestFootprint_IsNotTheResidentSize(t *testing.T) {
	f, err := NewFootprinter()
	require.NoError(t, err)
	fp0, err := f.Of(os.Getpid())
	require.NoError(t, err)
	rs0, err := f.residentOf(os.Getpid())
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "big")
	file, err := os.Create(path)
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	for i := range chunk {
		chunk[i] = byte(i)
	}
	for i := 0; i < 256; i++ {
		_, err := file.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, file.Sync())
	require.NoError(t, file.Close())
	rf, err := os.Open(path)
	require.NoError(t, err)
	defer rf.Close()
	data, err := syscall.Mmap(int(rf.Fd()), 0, 256<<20, syscall.PROT_READ, syscall.MAP_SHARED)
	require.NoError(t, err)
	defer syscall.Munmap(data)
	var sum byte
	for i := 0; i < len(data); i += 4096 {
		sum += data[i]
	}
	t.Logf("touched every page of the mapping (checksum %d: the loop must not be optimised away)", sum)

	fp1, err := f.Of(os.Getpid())
	require.NoError(t, err)
	rs1, err := f.residentOf(os.Getpid())
	require.NoError(t, err)
	if rs1 < rs0+200<<20 {
		t.Skipf("this system did not make the mapped pages resident (%d -> %d): nothing to tell the two figures apart by", rs0, rs1)
	}
	assert.Less(t, int64(fp1)-int64(fp0), int64(64<<20), "mapped file pages grew the resident size by %d MB but are not footprint", (rs1-rs0)>>20)
}
