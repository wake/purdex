package gate

// Module 2, slots: how many commands of each class run at once. A full suite runs one at a time and finishes sooner
// than two that share the cores (user decision D7, 2026-10-15).
type SlotsConfig struct {
	Heavy int    // full suites
	Light int    // targeted runs, so they do not wait behind a suite
	Order string // "fifo": the queue serves tickets in arrival order
}

func parseSlots(r raw) SlotsConfig {
	return SlotsConfig{
		Heavy: r.Int("heavy", 1, 1, 64),
		Light: r.Int("light", 3, 1, 64),
		Order: r.Enum("order", "fifo", "fifo"),
	}
}

// Size is the number of slots of a class.
func (s SlotsConfig) Size(c Class) int {
	if c == ClassHeavy {
		return s.Heavy
	}
	return s.Light
}
