package facts

import (
	"reflect"
	"testing"
)

func node(id int, cluster string) CPUNode {
	return CPUNode{LogicalID: id, HaveID: true, Cluster: cluster, HaveCluster: true}
}

func TestKindsFromNodes(t *testing.T) {
	// nodes in registry order, not core order: the number on the node decides, not the position
	got := KindsFromNodes([]CPUNode{node(2, "P"), node(0, "E"), node(1, "E"), node(3, "P")}, 4)
	if want := []string{"e", "e", "p", "p"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestKindsFromNodesGivesNothingWhenItCannotBeSure(t *testing.T) {
	good := []CPUNode{node(0, "E"), node(1, "P")}
	noID, noCluster := node(1, "P"), node(1, "P")
	noID.HaveID, noCluster.HaveCluster = false, false
	for name, c := range map[string]struct {
		nodes []CPUNode
		ncpu  int
	}{
		"fewer nodes than cores": {good[:1], 2},
		"more nodes than cores":  {append([]CPUNode{node(2, "P")}, good...), 2},
		"duplicate id":           {[]CPUNode{node(0, "E"), node(0, "P")}, 2},
		"id out of range":        {[]CPUNode{node(0, "E"), node(5, "P")}, 2},
		"negative id":            {[]CPUNode{node(0, "E"), node(-1, "P")}, 2},
		"no id":                  {[]CPUNode{node(0, "E"), noID}, 2},
		"no cluster-type":        {[]CPUNode{node(0, "E"), noCluster}, 2},
		"unknown cluster-type":   {[]CPUNode{node(0, "E"), node(1, "X")}, 2},
		"lower case":             {[]CPUNode{node(0, "e"), node(1, "p")}, 2},
		"no cores":               {nil, 0},
	} {
		if got := KindsFromNodes(c.nodes, c.ncpu); got != nil {
			t.Errorf("%s: got %v, want nil", name, got)
		}
	}
}
