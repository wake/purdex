package queue

// LogRetention and JobRetention are how long a job's log and a finished job are kept (spec §4.2). The result cache keeps an entry no
// longer than the log it points at, which is no longer than the job (store.CacheRetention; a test in the store pins the order).
const (
	LogRetention = logRetention
	JobRetention = retention
)
