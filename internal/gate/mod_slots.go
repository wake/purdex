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
		Heavy: r.Int("heavy", 1, 0, 64), // 0 pauses the class: new commands of it wait until the count is raised
		Light: r.Int("light", 3, 0, 64),
		Order: r.Enum("order", "fifo", "fifo"),
	}
}

// Paused says whether a class has no slot at all: a new command of it waits, never fails, and starts once the count is raised.
func (s SlotsConfig) Paused(c Class) bool { return s.Size(c) == 0 }

// Size is the number of slots of a class.
func (s SlotsConfig) Size(c Class) int {
	if c == ClassHeavy {
		return s.Heavy
	}
	return s.Light
}
