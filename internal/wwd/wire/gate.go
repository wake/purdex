package wire

// PassedDigestProto is the first proto whose readers take Summary.passed_digest (WWD-5b-2r): a writer fills the field only while
// PoolAtLeast(peers, PassedDigestProto) holds.
const PassedDigestProto = 3

// PoolAtLeast says whether every peer is reachable and reports proto >= min in its /v1/info (this host's own Proto is the build's and
// not in the list): the condition under which a writer may fill fields that proto min added (spec §13.1, the two phases).
//
// A peer that is not reachable holds the gate shut, whatever its last info said: that info is old, and the host may have been rolled
// back and be coming up as proto 1. A peer never reached carries the oldest proto (MinProto), so it holds the gate shut as well. An
// empty pool is true: there is nobody to be shown a field they would reject.
func PoolAtLeast(peers []Peer, min int) bool {
	for _, p := range peers {
		if !p.Reachable || p.Info.Proto < min {
			return false
		}
	}
	return true
}
