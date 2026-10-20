package facts

// The keys of an IOBlockStorageDriver's Statistics dictionary that the disk counters come from (the figures `ioreg -c
// IOBlockStorageDriver -r -l` prints).
const (
	ioStatReadBytes  = "Bytes (Read)"
	ioStatWriteBytes = "Bytes (Write)"
	ioStatReadOps    = "Operations (Read)"
	ioStatWriteOps   = "Operations (Write)"
)

// rawDiskIOFromProps is one disk's counters from what IOKit holds for it: the driver's Statistics (by key) and its IOMedia child's Whole
// flag and BSD Name. It is a disk only when the media is the whole disk (not a partition's) and has a name, and only when all four
// figures are there (a missing one is not a zero).
func rawDiskIOFromProps(stats map[string]uint64, whole bool, bsdName string) (RawDiskIO, bool) {
	if !whole || bsdName == "" {
		return RawDiskIO{}, false
	}
	rb, ok1 := stats[ioStatReadBytes]
	wb, ok2 := stats[ioStatWriteBytes]
	ro, ok3 := stats[ioStatReadOps]
	wo, ok4 := stats[ioStatWriteOps]
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return RawDiskIO{}, false
	}
	return RawDiskIO{Device: bsdName, ReadBytes: rb, WriteBytes: wb, ReadOps: ro, WriteOps: wo}, true
}
