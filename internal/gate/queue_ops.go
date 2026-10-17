package gate

import (
	"strconv"
	"time"
)

// The queue operations `pdx gate queue` makes (spec §5.2): list the waiters of both classes and move a ticket.

// Queue lists the live tickets of both classes in the order they are served; it removes nothing.
func (g *Gate) Queue() (heavy, light []Waiter) {
	cfg := g.Config()
	return g.pool(cfg, ClassHeavy).Waiters(), g.pool(cfg, ClassLight).Waiters()
}

// FindWaiters finds the waiting tickets a key names: a number is the pid of a waiting gate, anything else a ticket name. Both
// classes are searched.
func (g *Gate) FindWaiters(key string) []Waiter {
	heavy, light := g.Queue()
	pid, perr := strconv.Atoi(key)
	var out []Waiter
	for _, w := range append(heavy, light...) {
		if (perr == nil && w.PID == pid) || (perr != nil && w.Ticket == key) {
			out = append(out, w)
		}
	}
	return out
}

// MoveTicket sets a waiting ticket's rank (RankNormal clears it) and returns the rank it had. ErrNotWaiting when the ticket has gone.
func (g *Gate) MoveTicket(class Class, ticket string, rank Rank, by, reason string, until time.Time) (RankInfo, error) {
	pool := g.pool(g.Config(), class)
	if rank == RankNormal {
		return pool.ClearRank(ticket)
	}
	return pool.SetRank(ticket, rank, by, reason, until)
}

// RankBy is who a move is made by: the Claude Code session the gate reads from its environment, else the person at a terminal.
func (g *Gate) RankBy() string {
	if g.Session != "" {
		return g.Session
	}
	return "person"
}
