package wire

// PassedDigestProto is the first proto whose readers take Summary.passed_digest (WWD-5b-2r): a writer fills the field only while
// PoolAtLeast(peers, PassedDigestProto) holds.
const PassedDigestProto = 3

// PoolAtLeast says whether every peer reports proto >= min in its /v1/info (this host's own Proto is the build's and not in the list):
// the condition under which a writer may fill fields that proto min added (spec §13.1, the two phases).
//
// A peer that is not reachable counts by the last info it gave: a host that is mostly off (a laptop) would otherwise keep the gate shut
// for everyone, and the fields it gates (passed_digest, the liveness of a host) are wanted most while some host is down. The price is the
// window of a host that was rolled back while it was away: until the next poll reads its proto again, a field it would reject can go out.
// A peer never seen carries the oldest proto (MinProto), so it holds the gate shut. An empty pool is true: there is nobody to be shown a
// field they would reject.
func PoolAtLeast(peers []Peer, min int) bool {
	for _, p := range peers {
		if p.Info.Proto < min {
			return false
		}
	}
	return true
}
