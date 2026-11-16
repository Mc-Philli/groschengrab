package handlers

import (
	"database/sql"
	"os"
	"testing"
)

func accountID(t *testing.T, h *Handlers, name string) int64 {
	t.Helper()
	var id int64
	if err := h.db.QueryRow(`SELECT id FROM accounts WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("Konto %s: %v", name, err)
	}
	return id
}

func balance(h *Handlers, id int64) float64 {
	var b float64
	h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, id).Scan(&b)
	return b
}

func TestDeleteAccountWithTransactions(t *testing.T) {
	h := newDepotTestHandlers(t)
	giro, spar, bar := accountID(t, h, "Privatkonto"), accountID(t, h, "Sparkonto"), accountID(t, h, "Bargeld")

	book := func(acc int64, to int64, kind string, amount float64) {
		t.Helper()
		toID := sql.NullInt64{Int64: to, Valid: to != 0}
		if err := h.insertTransactionAndUpdateBalance(acc, toID, 1,
			transactionRecord{BookedAt: "2026-11-01", Kind: kind, Amount: amount}); err != nil {
			t.Fatal(err)
		}
	}
	book(spar, 0, "income", 1000)     // Beispielwert auf dem Sparkonto
	book(giro, 0, "income", 3000)     // bleibt
	book(giro, spar, "transfer", 200) // Umbuchung aufs Sparkonto
	book(spar, bar, "transfer", 50)   // Umbuchung vom Sparkonto ins Bargeld
	mustExec(t, h, `INSERT INTO recurring_transactions (account_id, to_account_id, user_id, kind, amount, start_date)
		VALUES (?, ?, 1, 'transfer', 100, '2026-12-01')`, giro, spar)
	mustExec(t, h, `INSERT INTO savings_plans (id, depot, amount, day_of_month, start_date, account_id, user_id)
		VALUES (1, 'WillBe', 500, 5, '2026-12-01', ?, 1)`, spar)

	name, n, err := h.deleteAccount(spar)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Sparkonto" || n != 3 {
		t.Errorf("gelöscht %q mit %d Buchungen, want Sparkonto mit 3", name, n)
	}
	// Die Umbuchungen sind auf den anderen Konten rückgängig gemacht.
	if b := balance(h, giro); b != 3000 {
		t.Errorf("Privatkonto = %v, want 3000", b)
	}
	if b := balance(h, bar); b != 0 {
		t.Errorf("Bargeld = %v, want 0", b)
	}
	var left, recurring int
	var planAccount sql.NullInt64
	h.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&left)
	h.db.QueryRow(`SELECT COUNT(*) FROM recurring_transactions`).Scan(&recurring)
	h.db.QueryRow(`SELECT account_id FROM savings_plans WHERE id = 1`).Scan(&planAccount)
	if left != 1 || recurring != 0 || planAccount.Valid {
		t.Errorf("übrig: %d Buchungen, %d Daueraufträge, Sparplan-Konto %v", left, recurring, planAccount)
	}

	if _, _, err := h.deleteAccount(spar); err != sql.ErrNoRows {
		t.Errorf("zweites Löschen: err = %v, want sql.ErrNoRows", err)
	}
}

func TestResetData(t *testing.T) {
	h := newDepotTestHandlers(t)
	giro := accountID(t, h, "Privatkonto")
	mustExec(t, h, `INSERT INTO accounts (name, type, balance) VALUES ('Beispielbank', 'Girokonto', 999)`)
	if err := h.insertTransactionAndUpdateBalance(giro, sql.NullInt64{}, 1,
		transactionRecord{BookedAt: "2026-11-01", Kind: "expense", Amount: 10}); err != nil {
		t.Fatal(err)
	}
	mustExec(t, h, `INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date) VALUES ('VT', 1, 100, '2026-01-01')`)
	mustExec(t, h, `INSERT INTO other_assets (name, type, current_value) VALUES ('Auto', 'vehicle', 5000)`)
	mustExec(t, h, `INSERT INTO categories (name) VALUES ('Lebensmittel')`)

	backup, err := h.safetyCopy("vor-reset")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("keine Sicherheitskopie geschrieben")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("Sicherheitskopie fehlt: %v", err)
	}

	if err := h.resetData(true); err != nil {
		t.Fatal(err)
	}
	count := func(table string) int {
		var n int
		h.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		return n
	}
	for _, table := range []string{"transactions", "holdings", "other_assets"} {
		if n := count(table); n != 0 {
			t.Errorf("%s: %d Zeilen, want 0", table, n)
		}
	}
	if n := count("users"); n != 1 {
		t.Errorf("Nutzer: %d, want 1 (Logins bleiben)", n)
	}
	if n := count("categories"); n != 1 {
		t.Errorf("Kategorien: %d, want 1 (behalten)", n)
	}
	accounts, _ := h.loadAccounts()
	if len(accounts) != 3 || accounts[0].Balance != 0 {
		t.Errorf("nach Reset: %+v, want 3 Standard-Konten mit Saldo 0", accounts)
	}

	if err := h.resetData(false); err != nil {
		t.Fatal(err)
	}
	if n := count("categories"); n != 0 {
		t.Errorf("Kategorien: %d, want 0", n)
	}
}
