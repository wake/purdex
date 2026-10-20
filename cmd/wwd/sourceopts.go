package main

import "github.com/wake/purdex/internal/wwd/facts"

// sourceOptions are the parts of the host's reads that are not for every host. The owner reading is taken on every host that can read it
// (macOS; Linux has no owner source and the facts' owner stays null): `[owner] enabled` decides whether the owner rule is in force (the
// owner_rule of the worker settings, main.go), not whether the owner is looked at. The monitoring page shows whether the owner is using the
// machine, and spec §3.2 says that with no rule the reading is still reported and never counted. ownerMinutes is the idle limit of the rule
// now in force (0: the default), asked at every sample.
func sourceOptions(toolDirs []string, ownerMinutes func() int) facts.SourceOptions {
	return facts.SourceOptions{ToolDirs: toolDirs, Owner: &facts.OwnerConfig{}, OwnerMinutes: ownerMinutes}
}
