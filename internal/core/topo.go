package core

import "fmt"

// topoSort sorts modules by dependency order using Kahn's algorithm.
func topoSort(modules []Module) ([]Module, error) {
	byName := make(map[string]Module, len(modules))
	inDegree := make(map[string]int, len(modules))
	dependents := make(map[string][]string)

	for _, m := range modules {
		byName[m.Name()] = m
		inDegree[m.Name()] = 0
	}

	for _, m := range modules {
		for _, dep := range m.Dependencies() {
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("module %q depends on unknown module %q", m.Name(), dep)
			}
			inDegree[m.Name()]++
			dependents[dep] = append(dependents[dep], m.Name())
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	var sorted []Module
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		sorted = append(sorted, byName[name])
		for _, dep := range dependents[name] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(modules) {
		return nil, fmt.Errorf("circular dependency detected among modules")
	}

	return sorted, nil
}
