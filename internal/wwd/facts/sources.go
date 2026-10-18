package facts

// SourceOptions switch on the parts that are not for every host.
type SourceOptions struct {
	// Owner turns the "owner is active" rule on (air26); nil leaves owner null.
	Owner *OwnerConfig
	// OwnerMinutes, when set, is asked at every sample for the idle limit in
	// minutes (0: the default), so the rule follows the worker settings at run time.
	OwnerMinutes func() int
	// ToolDirs are the resolved [tools] extra_path directories: searched first by the tool probe, and the same list leads a job's PATH.
	ToolDirs []string
}

// OSVersion is the host operating system's version ("26.6.2" on macOS, the
// os-release VERSION_ID on Linux); "" when unknown.
func OSVersion() string { return osVersion() }
