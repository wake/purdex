package main

import (
	"context"

	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/jobdb"
)

// perJobDB is what the job executor (PerJobDB) and the loop (Sweeper) take of the per-job database.
type perJobDB interface {
	Create(ctx context.Context, jobID string) (map[string]string, error)
	Drop(ctx context.Context, jobID string) error
	Sweep(ctx context.Context, live []string) error
}

// newPerJobDB is the per-job database of this host, or a nil interface when [jobdb] is off (a task that asks for db "per-job" is
// then rejected). It returns the interface, not *jobdb.PerJob, so that "off" stays nil after it is stored in another interface.
func newPerJobDB(c daemon.JobDBConfig) perJobDB {
	if !c.Enabled {
		return nil
	}
	ex := jobdb.CLI{DefaultsFile: c.DefaultsFile}
	return jobdb.NewPerJob(jobdb.New(ex, jobdb.Config{Host: c.Host, Port: c.Port, UserHosts: c.UserHosts}))
}
