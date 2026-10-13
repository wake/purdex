package resourcesmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// The second half of the crash-boundary table (the first half is in
// store_tx_test.go): the operations that end positions, pass offers, name
// rows and reconcile also leave neither table changed when they fail between
// their statements.
func TestTx_FailureBetweenStatementsLeavesNeither_Ends(t *testing.T) {
	type op struct {
		name  string
		setup func(t *testing.T, s *leaseStore)
		do    func(s *leaseStore) error
	}
	ops := []op{
		{"EndPositionWithRows", func(t *testing.T, s *leaseStore) { queuedFixture(t, s) }, func(s *leaseStore) error {
			_, err := s.EndPositionWithRows("pos_00000001", 3000, endPosition{Reason: resources.EndCancelledBySession, From: []string{resources.PosQueued}})
			return err
		}},
		{"EndRowMaybePosition", func(t *testing.T, s *leaseStore) { queuedFixture(t, s) }, func(s *leaseStore) error {
			_, err := s.EndRowMaybePosition("a", resources.EndCancelledBySession, 3000, "")
			return err
		}},
		{"PassOffer", func(t *testing.T, s *leaseStore) {
			queuedFixture(t, s)
			_, err := s.OfferPosition("pos_00000001", 1000, "", "", "")
			require.NoError(t, err)
		}, func(s *leaseStore) error {
			_, err := s.PassOffer("pos_00000001", 1000, 5000)
			return err
		}},
		{"NameRows", func(t *testing.T, s *leaseStore) {
			runningFixture(t, s)
			require.NoError(t, createRow(s, txRow("b", "s1", "pos_00000001", resources.CallQueue, "x", "/w", 1200)))
			setState(t, s, "pos_00000001", "state = 'idle', window_until = 9000")
		}, func(s *leaseStore) error {
			_, err := s.NameRows("pos_00000001", 1400, "idle")
			return err
		}},
		{"ReconcilePositions", func(t *testing.T, s *leaseStore) {
			require.NoError(t, s.insertPosition(positionRow{ID: "pos_00000001", SessionID: "s1", State: resources.PosQueued, CreatedAt: 1}))
			setState(t, s, "pos_00000001", "state = 'running'")
		}, func(s *leaseStore) error {
			_, err := s.ReconcilePositions(10_000)
			return err
		}},
	}
	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			s, path := fileStore(t)
			o.setup(t, s)
			before := dumpTables(t, s.db)
			s.afterFirstStmt = func() error { return errCrash }
			require.ErrorIs(t, o.do(s), errCrash)
			require.NoError(t, s.Close())
			s2, err := openLeaseStore(path)
			require.NoError(t, err)
			defer s2.Close()
			assert.Equal(t, before, dumpTables(t, s2.db), "an operation that failed between its statements changed nothing")
		})
	}
}
