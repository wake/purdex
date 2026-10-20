package facts

import "github.com/wake/purdex/internal/wwd/wire"

// CPUNode is what the IORegistry says about one cpuN node under IODeviceTree:/cpus: the logical CPU number host_processor_info
// counts by, and the cluster-type ("E" or "P"). A Have flag is false where the property was absent or not the expected type.
type CPUNode struct {
	LogicalID   int
	HaveID      bool
	Cluster     string
	HaveCluster bool
}

// KindsFromNodes lines the nodes up with the cores 0..ncpu-1. It answers only when every core has exactly one node that names it
// and a cluster-type that is "E" or "P"; any gap, duplicate, surplus or unknown value gives nil for all cores. The order of the
// nodes, and the number of an efficiency or performance core, are never used to fill a hole: a wrong "p" on a chart is worse than none.
func KindsFromNodes(nodes []CPUNode, ncpu int) []string {
	if ncpu <= 0 || len(nodes) != ncpu {
		return nil
	}
	kinds := make([]string, ncpu)
	for _, n := range nodes {
		if !n.HaveID || n.LogicalID < 0 || n.LogicalID >= ncpu || kinds[n.LogicalID] != "" || !n.HaveCluster {
			return nil
		}
		switch n.Cluster {
		case "E":
			kinds[n.LogicalID] = wire.CoreKindE
		case "P":
			kinds[n.LogicalID] = wire.CoreKindP
		default:
			return nil
		}
	}
	return kinds
}
