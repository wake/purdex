package push

// AlertsReadableKey is the service-registry key under which the alert deliverer (the wwdalerts module) publishes AlertsReadable.
const AlertsReadableKey = "wwdalerts.alerts-readable"

// AlertsReadable says whether the alert deliverer could read its own host's wwd at its last pass. A daemon that cannot read it reads no
// alert from any host, so it delivers nothing: the fleet role answer folds this into push_ready, which is what lets a standby take over
// for a primary whose wwd is down (spec §13.3). It is true until the first pass has run, so a restart does not hand delivery over for a
// moment.
type AlertsReadable interface {
	AlertsReadable() bool
}
