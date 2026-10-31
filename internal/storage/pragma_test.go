package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpen_PragmasOnEveryConnection(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "p.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Hold several connections at once so the pool has to open new ones.
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var fk, bt int
		_ = conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk)
		_ = conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&bt)
		if fk != 1 || bt != 5000 {
			t.Errorf("connection %d has foreign_keys=%d busy_timeout=%d", i, fk, bt)
		}
	}
}
