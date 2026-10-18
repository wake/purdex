package wire

// PoolAtLeast says whether every peer reports proto >= min in its /v1/info (this host's own Proto is the build's and not in the
// list): the condition under which a writer may fill fields that proto min added (spec §13.1, the two phases). A peer never
// reached carries the oldest proto (MinProto), so it holds the gate shut; so does a host rolled back below min, which is why the
// writers stop again when one is. An empty pool is true: there is nobody to be shown a field they would reject.
func PoolAtLeast(peers []Peer, min int) bool {
	for _, p := range peers {
		if p.Info.Proto < min {
			return false
		}
	}
	return true
}
