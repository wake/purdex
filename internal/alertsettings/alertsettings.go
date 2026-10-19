// Package alertsettings is the narrow read of the host setting `wwd_alerts`, for the module that delivers the wwd host alerts and cannot
// import hostconfig (wwdalerts, WWD-8a-2a-0).
package alertsettings

// Key is the service-registry key under which the hostconfig module publishes itself as a Reader.
const Key = "hostconfig.wwd-alerts-settings"

// Settings is the host setting `wwd_alerts`. Each daemon keeps its own: the deliverer is whichever of mlab's and air26's holds the fleet
// primary role, so to silence the phone both are set.
type Settings struct {
	// Push: the push-level alerts may be sent to the phone. false (the default) writes and gathers them and sends none: a person turns
	// it on once they have seen how many there would be.
	Push bool `json:"push"`
}

// Default is what a never-written setting answers: off.
func Default() Settings { return Settings{} }

// Reader is what a module type-asserts on the registry value. A stored value that no longer reads is an error, not a silent default:
// the caller decides what to do without a setting (the delivery takes it as off).
type Reader interface {
	WwdAlertsSettings() (Settings, error)
}
