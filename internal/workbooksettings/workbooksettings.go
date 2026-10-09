// Package workbooksettings is the narrow read of the host setting `workbook`, for modules that cannot import hostconfig
// (the session workbook module, and the push module that holds a Stop push for it).
package workbooksettings

// Key is the service-registry key under which the hostconfig module publishes itself as a Reader.
const Key = "hostconfig.workbook-settings"

// DefaultPushWaitS is how long a Stop push waits for its workbook line.
const DefaultPushWaitS = 8

// MaxPushWaitS is the longest wait a host may set.
const MaxPushWaitS = 30

// Settings is the host setting `workbook` (spec §7). There is no enabled switch (plan D7).
type Settings struct {
	// PushWaitS: seconds a Stop push may be held for the entry's push line; 0 = never wait.
	PushWaitS int `json:"push_wait_s"`
}

// Default is what a never-written setting answers.
func Default() Settings { return Settings{PushWaitS: DefaultPushWaitS} }

// Reader is what a module type-asserts on the registry value. A stored value that no longer reads is an error, not a
// silent default: the caller decides what to do without a setting.
type Reader interface {
	WorkbookSettings() (Settings, error)
}
