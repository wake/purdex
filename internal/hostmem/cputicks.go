package hostmem

// CoreTicks are one core's ticks since boot, as host_processor_info gives them.
type CoreTicks struct {
	User, System, Idle, Nice uint64
}

// SumTicks adds every core's ticks: busy (user + system + nice) and total (those and idle).
func SumTicks(cores []CoreTicks) (busy, total uint64) {
	for _, c := range cores {
		b := c.User + c.System + c.Nice
		busy += b
		total += b + c.Idle
	}
	return busy, total
}
