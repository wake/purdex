package derived

import "github.com/wake/purdex/internal/wwd/wire"

// Why headroom is 0 (spec §3.2). Compute decides headroom from these reasons, so a side has room exactly when it has no reason; Why
// reads the same judgement back from a facts record for a reader that was not there when it was made.

const (
	ReasonWorkerOff   = "worker_off"
	ReasonZoneWarning = "zone_warning"
	ReasonZoneDanger  = "zone_danger"
	ReasonZoneUnknown = "zone_unknown"
	ReasonExceptionP  = "exception:" // + the exception's name
	ReasonOwnerActive = "owner_active"
	ReasonDiskLow     = "disk_low" // the work volume has less free space than the worker's disk floor; heavy only
	ReasonSlotsFull   = "slots_full"
	// ReasonGateFull is no longer produced (WWD-9: the pdx gate is gone); the string stays because an older App or peer still knows it.
	ReasonGateFull = "gate_full"
	// ReasonUnknown is a side at 0 that nothing in the record explains; only Why gives it (Compute always knows).
	ReasonUnknown = "unknown"
)

// judgement is what the reasons are made from.
type judgement struct {
	workerOff bool
	zone      wire.Zone
	holds     []Hold
	owner     bool
	diskLow   bool       // the work volume is under the disk floor: holds heavy
	free      wire.Slots // slots less what runs
}

// reasons lists, in the spec's order, what holds each side. A side with none has room.
func (j judgement) reasons() wire.HeadroomReasons {
	heavy, light := []string{}, []string{}
	if j.workerOff {
		heavy, light = append(heavy, ReasonWorkerOff), append(light, ReasonWorkerOff)
	}
	switch j.zone {
	case wire.ZoneNormal:
	case wire.ZoneWarning:
		heavy = append(heavy, ReasonZoneWarning)
	case wire.ZoneDanger:
		heavy, light = append(heavy, ReasonZoneDanger), append(light, ReasonZoneDanger)
	default:
		heavy = append(heavy, ReasonZoneUnknown)
	}
	for _, h := range j.holds {
		heavy = append(heavy, ReasonExceptionP+h.Name)
		if h.Light {
			light = append(light, ReasonExceptionP+h.Name)
		}
	}
	if j.owner {
		heavy, light = append(heavy, ReasonOwnerActive), append(light, ReasonOwnerActive)
	}
	if j.diskLow {
		heavy = append(heavy, ReasonDiskLow)
	}
	if j.free.Heavy <= 0 {
		heavy = append(heavy, ReasonSlotsFull)
	}
	if j.free.Light <= 0 {
		light = append(light, ReasonSlotsFull)
	}
	return wire.HeadroomReasons{Heavy: heavy, Light: light}
}

// Why is the reasons of a facts record. A record does not carry which exception holds light work, so that is read as what is left:
// light at 0 with slots free, no zone or owner reason and some exceptions is those exceptions. worker_off is the worker's own flag and does not touch the headroom the record states.
func Why(f wire.Facts) wire.HeadroomReasons {
	j := judgement{
		workerOff: !f.Worker.Enabled, zone: f.Derived.Zone, owner: f.Worker.OwnerRule != "" && f.Owner != nil && f.Owner.Active, // as Compute is fed: a reading counts only with a rule in force
		diskLow: f.Derived.DiskLow,
		free:    wire.Slots{Heavy: free(f.Worker.Slots.Heavy, f.Worker.Running.Heavy), Light: free(f.Worker.Slots.Light, f.Worker.Running.Light)},
	}
	for _, e := range f.Exception {
		j.holds = append(j.holds, Hold{Name: e.Name})
	}
	// what blocks light apart from the exceptions: if that is nothing and the record still says 0, the exceptions did it
	if f.Derived.Headroom.Light == 0 && len(j.holds) > 0 && j.reasonsLightIgnoringHolds() == 0 {
		for i := range j.holds {
			j.holds[i].Light = true
		}
	}
	r := j.reasons()
	// a side at 0 for no reason the record shows (a skewed or faulty peer, or an older record that was held by a gate this build no
	// longer has): never an empty list, which means "there is room".
	if f.Derived.Headroom.Heavy == 0 && len(r.Heavy) == 0 {
		r.Heavy = append(r.Heavy, ReasonUnknown)
	}
	if f.Derived.Headroom.Light == 0 && len(r.Light) == 0 {
		r.Light = append(r.Light, ReasonUnknown)
	}
	return r
}

// reasonsLightIgnoringHolds counts the light reasons that do not come from an exception.
func (j judgement) reasonsLightIgnoringHolds() int {
	j.holds = nil
	return len(j.withoutWorker().reasons().Light)
}

func (j judgement) withoutWorker() judgement {
	j.workerOff = false
	return j
}

// ComputeWithReasons is Compute, and the reasons it was decided by (exact).
func ComputeWithReasons(l *ZoneLatch, in Input) (wire.Derived, wire.HeadroomReasons) {
	zone := l.Update(in.Pressure, in.PageRate, in.RateLimit, in.PressureKnown)
	j := judgement{
		zone: zone, holds: in.Exceptions, owner: in.OwnerActive, diskLow: in.DiskLow,
		free: wire.Slots{Heavy: free(in.Slots.Heavy, in.Running.Heavy), Light: free(releaseLightSlots(in.Slots.Light, in.Exceptions), in.Running.Light)},
	}
	r := j.reasons()
	d := wire.Derived{Zone: zone, DiskLow: in.DiskLow}
	if len(r.Heavy) == 0 {
		d.Headroom.Heavy = j.free.Heavy
	}
	if len(r.Light) == 0 {
		d.Headroom.Light = j.free.Light
	}
	if zone == wire.ZoneNormal {
		// Initial formula (spec §16, tuned by observation): available memory, less the
		// reserve and a quarter of what the compressor already holds (squeezed
		// memory comes back as pressure when sessions grow), over 512 MB a session.
		usable := in.AvailableMB - sessionReserveMB - in.CompressorMB/4
		d.SessionsRoom = min(max(usable/sessionMB, 0), sessionsCap)
	}
	return d, r
}
