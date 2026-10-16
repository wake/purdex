package gate

import "time"

// Module 6, packaging (#2996): a script that packages or builds an output (isPackagingScript) starts at once and is never held
// back (user 10-15), but it can run for minutes with a gigabyte behind it. When it has run for LateSlotAfter the gate puts it
// under a slot of LateSlotClass until it ends: nothing is stopped and nothing is asked of admission; if the pool is full it is
// over its size for as long as the packaging lasts, and the people behind it queue. A script the table does not know is only
// timed (RecordUnknown): a dev server would otherwise hold a slot for ever.
type PackagingConfig struct {
	LateSlotAfter time.Duration // 0 = off: packaging is not put under a slot
	LateSlotClass Class         // the pool the late slot is taken from
	RecordUnknown time.Duration // an unknown `run` script that ran at least this long leaves a line in decisions.jsonl; 0 = never
}

func parsePackaging(r raw) PackagingConfig {
	return PackagingConfig{
		LateSlotAfter: time.Duration(r.Int("late_slot_after_s", 60, 0, 24*3600)) * time.Second,
		LateSlotClass: Class(r.Enum("late_slot_class", "light", "light", "heavy")),
		RecordUnknown: time.Duration(r.Int("record_unknown_s", 10, 0, 3600)) * time.Second,
	}
}
