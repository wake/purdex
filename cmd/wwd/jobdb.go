package main

import (
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/jobdb"
)

// newPerJobDB is the per-job database of this host, or nil when [jobdb] is off (a task that asks for db "per-job" is then
// rejected). The result is both the job executor's PerJobDB and a loop Sweeper.
func newPerJobDB(c daemon.JobDBConfig) *jobdb.PerJob {
	if !c.Enabled {
		return nil
	}
	ex := jobdb.CLI{DefaultsFile: c.DefaultsFile}
	return jobdb.NewPerJob(jobdb.New(ex, jobdb.Config{Host: c.Host, Port: c.Port, UserHosts: c.UserHosts}))
}
