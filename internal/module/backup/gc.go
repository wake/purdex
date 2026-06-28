package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

const (
	// maxSnapshots is the latest-N arm of the retention keep-set.
	maxSnapshots = 100
	// maxAgeDays is the age arm of the retention keep-set.
	maxAgeDays = 90
	// blobGraceSeconds is the upload grace period: an unreferenced blob younger
	// than this is never reaped (protects an in-flight PUT before its snapshot).
	blobGraceSeconds = 3600 // 1 h
)

// GC runs garbage collection across all store_ids in one BEGIN IMMEDIATE
// transaction using the given clock. Best-effort on Start().
func (s *BackupStore) GC(now int64) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	rows, err := conn.QueryContext(ctx, `SELECT DISTINCT store_id FROM backup_snapshots`)
	if err != nil {
		return err
	}
	var stores []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		stores = append(stores, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, st := range stores {
		if err := s.gcSnapshots(ctx, conn, st, now); err != nil {
			return err
		}
	}
	// Blob sweep is global (blobs are not store-scoped); run once after all
	// stores have had their surplus snapshots removed.
	if err := s.gcBlobs(ctx, conn, now); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// gcTx runs GC for one store inside an existing transaction (the append path):
// drop that store's surplus snapshots, then sweep unreferenced blobs globally.
func (s *BackupStore) gcTx(ctx context.Context, conn *sql.Conn, storeID string, now int64) error {
	if err := s.gcSnapshots(ctx, conn, storeID, now); err != nil {
		return err
	}
	return s.gcBlobs(ctx, conn, now)
}

// gcSnapshots deletes snapshots for storeID that fall outside the keep-set:
// (latest 100 by id) ∪ (within 90 days) ∪ (ancestor closure of that set). The
// union is a retention window, NOT a hard cap (a 90-day burst of distinct
// states is allowed to grow).
func (s *BackupStore) gcSnapshots(ctx context.Context, conn *sql.Conn, storeID string, now int64) error {
	rows, err := conn.QueryContext(ctx,
		`SELECT id, parent_id, created_at FROM backup_snapshots WHERE store_id = ?`, storeID)
	if err != nil {
		return err
	}
	type snap struct {
		id        int64
		createdAt int64
	}
	var snaps []snap
	parentOf := map[int64]int64{} // 0 = no parent
	for rows.Next() {
		var id, createdAt int64
		var parent sql.NullInt64
		if err := rows.Scan(&id, &parent, &createdAt); err != nil {
			rows.Close()
			return err
		}
		snaps = append(snaps, snap{id: id, createdAt: createdAt})
		if parent.Valid {
			parentOf[id] = parent.Int64
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(snaps) == 0 {
		return nil
	}

	keep := make(map[int64]bool, len(snaps))
	// Age arm: within 90 days.
	threshold := now - int64(maxAgeDays)*86400
	ids := make([]int64, 0, len(snaps))
	for _, sn := range snaps {
		ids = append(ids, sn.id)
		if sn.createdAt >= threshold {
			keep[sn.id] = true
		}
	}
	// Latest-N arm: top maxSnapshots by id.
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	for i := 0; i < len(ids) && i < maxSnapshots; i++ {
		keep[ids[i]] = true
	}
	// Ancestor closure: walk parent_id up from every kept node.
	for _, sn := range snaps {
		if !keep[sn.id] {
			continue
		}
		for p := parentOf[sn.id]; p != 0 && !keep[p]; p = parentOf[p] {
			keep[p] = true
		}
	}

	for _, sn := range snaps {
		if keep[sn.id] {
			continue
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM backup_snapshots WHERE id = ?`, sn.id); err != nil {
			return err
		}
	}
	return nil
}

// gcBlobs deletes blobs referenced by no surviving snapshot (any store) whose
// created_at is older than the grace period.
func (s *BackupStore) gcBlobs(ctx context.Context, conn *sql.Conn, now int64) error {
	// Referenced hashes across ALL surviving snapshots.
	mrows, err := conn.QueryContext(ctx, `SELECT manifest FROM backup_snapshots`)
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for mrows.Next() {
		var mj string
		if err := mrows.Scan(&mj); err != nil {
			mrows.Close()
			return err
		}
		var entries []ManifestEntry
		if err := json.Unmarshal([]byte(mj), &entries); err != nil {
			continue // tolerate an unreadable manifest rather than abort GC
		}
		for _, e := range entries {
			if e.Kind == "file" && e.Hash != "" {
				referenced[e.Hash] = true
			}
		}
	}
	if err := mrows.Err(); err != nil {
		mrows.Close()
		return err
	}
	mrows.Close()

	graceThreshold := now - blobGraceSeconds
	brows, err := conn.QueryContext(ctx, `SELECT hash, created_at FROM backup_blobs`)
	if err != nil {
		return err
	}
	var toDelete []string
	for brows.Next() {
		var hash string
		var createdAt int64
		if err := brows.Scan(&hash, &createdAt); err != nil {
			brows.Close()
			return err
		}
		if !referenced[hash] && createdAt < graceThreshold {
			toDelete = append(toDelete, hash)
		}
	}
	if err := brows.Err(); err != nil {
		brows.Close()
		return err
	}
	brows.Close()

	for _, hash := range toDelete {
		if _, err := conn.ExecContext(ctx, `DELETE FROM backup_blobs WHERE hash = ?`, hash); err != nil {
			return err
		}
	}
	return nil
}
