package handlers

import (
	"testing"

	"household-app/internal/db"
)

func TestDefaultAccountsOnFreshDatabase(t *testing.T) {
	h := newTestHandlers(t)

	accounts, err := h.loadAccounts()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, a := range accounts {
		names[a.Name] = true
		if a.Balance != 0 {
			t.Errorf("%s: Saldo %v, want 0", a.Name, a.Balance)
		}
	}
	for _, want := range []string{"Privatkonto", "Sparkonto", "Bargeld"} {
		if !names[want] {
			t.Errorf("Standard-Konto %q fehlt (vorhanden: %v)", want, names)
		}
	}
	if len(accounts) != 3 {
		t.Errorf("%d Konten, want 3", len(accounts))
	}

	// Ein erneuter Start darf die Konten nicht noch einmal anlegen.
	if err := db.RunMigrations(h.db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	again, _ := h.loadAccounts()
	if len(again) != 3 {
		t.Errorf("nach erneutem Start %d Konten, want 3", len(again))
	}
}
