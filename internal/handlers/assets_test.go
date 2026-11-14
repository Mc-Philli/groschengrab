package handlers

import (
	"path/filepath"
	"testing"

	"household-app/internal/db"
)

func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.RunMigrations(database, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return New(database, nil)
}

func TestAssetValuesByDate(t *testing.T) {
	h := newTestHandlers(t)

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := h.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO other_assets (id, name, type, current_value, updated_at) VALUES (1, 'Säule 3a', 'pension', 12000, '2026-03-01')`)
	mustExec(`INSERT INTO other_assets (id, name, type, current_value, updated_at) VALUES (2, 'Kredit', 'debt', 4000, '2026-02-01')`)
	mustExec(`INSERT INTO asset_valuations (asset_id, valued_at, value) VALUES (1, '2026-01-01', 10000), (1, '2026-03-01', 12000), (2, '2026-02-01', 4000)`)

	dates := []string{"2025-12-31", "2026-01-01", "2026-02-15", "2026-03-01", "2026-04-01"}
	got, err := h.assetValuesByDate(dates)
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 10000, 6000, 8000, 8000}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: got %v, want %v", dates[i], got[i], want[i])
		}
	}

	total, err := h.otherAssetsTotal()
	if err != nil {
		t.Fatal(err)
	}
	if total != 8000 {
		t.Errorf("otherAssetsTotal = %v, want 8000", total)
	}
}
