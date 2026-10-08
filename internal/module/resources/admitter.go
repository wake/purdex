package resourcesmod

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

// Admitter decides which waiters may start. The formula lives behind it so
// the lead's pending decision on D-2 changes one implementation, not the pass.
type Admitter interface {
	Admit(in AdmitInput) (grant, overrun []string)
}

// AdmitInput is everything one admission decision is made from. Nothing in it
// is changed by the Admitter.
type AdmitInput struct {
	// Host and Available are the module's latest sample; Available is false
	// while warming up, after three failed samples, or when unsupported.
	Host      resources.HostUse
	Available bool
	// Leases are the held leases, LeaseUse each one's latest raw measured use
	// by id (empty until the per-lease measurement lands).
	Leases   []resources.Lease
	LeaseUse map[string]float64
	// Waiters are the queued requests.
	Waiters  []resources.Waiter
	Now      time.Time
	Settings resources.Settings
}

// additiveAdmitter is resources.Admit: committed charges plus the host's
// unleased use plus the waiter's weight must fit in the capacity.
type additiveAdmitter struct{}

func (additiveAdmitter) Admit(in AdmitInput) (grant, overrun []string) {
	return resources.Admit(in.Host, in.Available, in.Leases, in.LeaseUse, in.Waiters, in.Now, in.Settings)
}
