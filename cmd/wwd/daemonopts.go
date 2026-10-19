package main

import (
	"net/http"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/jobsapi"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
)

// daemonParts is what the daemon's options are made of.
type daemonParts struct {
	HostID, Alias, Token string
	Cfg                  daemon.Config
	Hooks                *fault.Hooks
	Sampler              *daemon.Sampler
	Peers                []daemon.PeerAddr
	State                *daemon.HostState
	Broker               *daemon.Broker
	Queue                *queue.Queue
	FastLane             *localruns.Service
	Observer             *daemon.Observer
	Store                *store.Store
}

// daemonOptions is the daemon's options. LocalRuns and ClaimRoutes are set together: the claim routes on the socket are what the host's
// own worker and the fast-lane runners claim, heartbeat and report with, and POST /v1/local-runs opens only together with them.
func daemonOptions(p daemonParts) daemon.Options {
	repos := clones.NewSource(p.Cfg.Repos)
	return daemon.Options{
		HostID: p.HostID, Alias: p.Alias, Version: buildinfo.Version,
		Token: p.Token, Fault: p.Hooks, WorkerEnabled: p.Cfg.Worker.Enabled, Sampler: p.Sampler, Peers: p.Peers,
		State: p.State, Broker: p.Broker, Queue: p.Queue, LocalRuns: p.FastLane, RepoSource: repos.Names,
		ReferenceTasks: newRefTaskSource(repos).Tasks, // the tasks whose qualify.reference is this host, read from the clones' manifests
		Earned:         p.Store,                       // task labels this host earned or lost (spec §7): the earned ones are advertised; GET /v1/earned-labels on the socket
		ClaimRoutes:    func(mux *http.ServeMux) { jobsapi.New(p.Queue).WithFault(p.Hooks).WorkerRoutes(mux, false) },
		Observer:       p.Observer,
		Alerts:         p.Store, // GET /v1/alerts (spec §13.3): read only until the alert rules write the table (8a-1, 8a-2)
		Usage:          p.Store, // GET /v1/usage on the socket: what finished jobs used (rows are written by the job-end hook, WWD usage U3)
	}
}
