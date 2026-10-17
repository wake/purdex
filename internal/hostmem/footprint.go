package hostmem

// Footprints is the physical footprint in bytes of each of pids that has one; a pid that has none (gone, or another user's), or no
// Footprinter at all, is left out, and the caller falls back to the resident size for it (#3057).
func Footprints(f *Footprinter, pids []int) map[int]uint64 {
	out := make(map[int]uint64, len(pids))
	if f == nil {
		return out
	}
	for _, pid := range pids {
		if v, err := f.Of(pid); err == nil {
			out[pid] = v
		}
	}
	return out
}
