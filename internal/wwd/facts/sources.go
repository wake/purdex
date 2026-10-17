package facts

// SourceOptions switch on the parts that are not for every host.
type SourceOptions struct {
	// Owner turns the "owner is active" rule on (air26); nil leaves owner null.
	Owner *OwnerConfig
}

// OSVersion is the host operating system's version ("26.6.2" on macOS, the
// os-release VERSION_ID on Linux); "" when unknown.
func OSVersion() string { return osVersion() }
