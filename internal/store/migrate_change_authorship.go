package store

import (
	"context"
	"database/sql"
	"fmt"
)

// upChangeAuthorship re-attributes this instance's own server-authored sync
// changes to its local device.
//
// Changes were being authored under the server device, but only the local
// device has a signing key: it is the identity bound to the libp2p keypair, the
// one SetSigner installs a key for, and the one whose public key peers learn
// during pairing. Server-authored changes were therefore stored unsigned, and a
// peer receiving them saw an author it had no key for, refused them as
// unverifiable, and got them again on every anti-entropy round -- forever. The
// change log never converged, even though file sync did.
//
// Fixing the authoring path (sync.AuthorDeviceID) only helps new changes; the
// rows already written stay unsyncable until they are re-attributed here.
//
// Only rows authored by *this* instance's server device are touched. A peer's
// server-authored rows, relayed to us, belong to that peer and are left alone.
func upChangeAuthorship(ctx context.Context, tx *sql.Tx) error {
	serverID, err := settingValue(ctx, tx, "server_device_id")
	if err != nil {
		return err
	}
	localID, err := settingValue(ctx, tx, "local_device_id")
	if err != nil {
		return err
	}
	// Nothing to move, or no distinct local identity to move it to. A fresh
	// database hits this and is left untouched.
	if serverID == "" || localID == "" || serverID == localID {
		return nil
	}

	// seq is per-device and must stay strictly increasing per author, so the
	// moved rows are renumbered onto the end of the local device's seq space
	// rather than keeping numbers that may already be taken.
	var maxLocalSeq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM sync_change WHERE device_id = ?`, localID,
	).Scan(&maxLocalSeq); err != nil {
		return fmt.Errorf("read local seq: %w", err)
	}

	// Read the revisions first, then renumber them one by one. A single UPDATE
	// with a correlated subquery over sync_change would be reading the table it
	// is rewriting, which is exactly the kind of thing that works until it
	// quietly does not.
	rows, err := tx.QueryContext(ctx,
		`SELECT revision FROM sync_change WHERE device_id = ? ORDER BY revision`, serverID)
	if err != nil {
		return fmt.Errorf("list server changes: %w", err)
	}
	var revisions []int64
	for rows.Next() {
		var rev int64
		if err := rows.Scan(&rev); err != nil {
			rows.Close()
			return err
		}
		revisions = append(revisions, rev)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// Revision order preserves the rows' relative ordering. sig is cleared
	// because these rows were never signed and the new seq is covered by the
	// signature anyway; BackfillLocalSignatures signs them on the next boot.
	for i, rev := range revisions {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sync_change SET device_id = ?, seq = ?, sig = '' WHERE revision = ?`,
			localID, maxLocalSeq+int64(i)+1, rev); err != nil {
			return fmt.Errorf("reattribute change %d: %w", rev, err)
		}
	}

	// Move the vector entry to match, so peers are told the right high-water
	// mark for the local device and stop being offered the server device's.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sync_vector (device_id, seq, hlc, updated_at)
SELECT ?, COALESCE(MAX(seq), 0), COALESCE(MAX(hlc), 0), unixepoch()
  FROM sync_change WHERE device_id = ?
ON CONFLICT(device_id) DO UPDATE SET
  seq = excluded.seq, hlc = MAX(sync_vector.hlc, excluded.hlc), updated_at = unixepoch()`,
		localID, localID); err != nil {
		return fmt.Errorf("update local vector: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_vector WHERE device_id = ?`, serverID); err != nil {
		return fmt.Errorf("drop server vector: %w", err)
	}
	return nil
}

// settingValue reads one settings row, treating a missing row as empty.
func settingValue(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var v string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}
