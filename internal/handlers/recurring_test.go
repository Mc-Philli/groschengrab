package handlers

import (
	"database/sql"
	"testing"

	"household-app/internal/models"
)

func TestNextRecurringDate(t *testing.T) {
	cases := []struct {
		start, last, end string
		every            int
		until, want      string
	}{
		{"2026-11-25", "", "", 1, "2026-12-31", "2026-11-25"},
		{"2026-11-25", "2026-11-25", "", 1, "2026-12-31", "2026-12-25"},
		{"2026-11-25", "2026-11-25", "", 1, "2026-12-24", ""},
		{"2026-01-31", "2026-01-31", "", 1, "2026-12-31", "2026-02-28"}, // Monatsende
		{"2026-01-31", "2026-02-28", "", 1, "2026-12-31", "2026-03-31"}, // danach wieder der 31.
		{"2026-01-15", "2026-01-15", "", 3, "2026-12-31", "2026-04-15"}, // vierteljährlich
		{"2026-03-01", "2026-03-01", "", 12, "2030-12-31", "2027-03-01"},
		{"2026-01-01", "2026-03-01", "2026-03-31", 1, "2026-12-31", ""}, // Enddatum erreicht
	}
	for _, c := range cases {
		r := models.RecurringTransaction{StartDate: c.start, LastExecutedOn: c.last, EndDate: c.end, EveryMonths: c.every}
		if got := nextRecurringDate(r, c.until); got != c.want {
			t.Errorf("start %s last %q every %d: got %q, want %q", c.start, c.last, c.every, got, c.want)
		}
	}
}

func TestRecurringCatchUpAndBalances(t *testing.T) {
	h := newDepotTestHandlers(t)
	mustExec(t, h, `UPDATE accounts SET balance = 1000 WHERE name = 'Privatkonto'`)
	var giro, spar int64
	h.db.QueryRow(`SELECT id FROM accounts WHERE name = 'Privatkonto'`).Scan(&giro)
	h.db.QueryRow(`SELECT id FROM accounts WHERE name = 'Sparkonto'`).Scan(&spar)

	mustExec(t, h, `INSERT INTO recurring_transactions (account_id, user_id, kind, amount, description, start_date)
		VALUES (?, 1, 'income', 5000, 'Lohn', '2026-09-25')`, giro)
	mustExec(t, h, `INSERT INTO recurring_transactions (account_id, user_id, kind, amount, description, start_date)
		VALUES (?, 1, 'expense', 1800, 'Miete', '2026-10-01')`, giro)
	mustExec(t, h, `INSERT INTO recurring_transactions (account_id, to_account_id, user_id, kind, amount, start_date)
		VALUES (?, ?, 1, 'transfer', 500, '2026-11-26')`, giro, spar)

	n, errs := h.RunDueRecurring("2026-11-16")
	// Lohn: 25.9., 25.10. · Miete: 1.10., 1.11. · Transfer: noch nicht fällig.
	if n != 4 || len(errs) > 0 {
		t.Fatalf("n=%d errs=%v, want 4", n, errs)
	}
	if n, _ := h.RunDueRecurring("2026-11-16"); n != 0 {
		t.Fatalf("zweiter Lauf bucht %d, want 0", n)
	}
	if n, _ := h.RunDueRecurring("2026-11-30"); n != 2 { // Lohn 25.11., Transfer 26.11.
		t.Fatalf("Lauf Ende November bucht %d, want 2", n)
	}

	var gBal, sBal float64
	h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, giro).Scan(&gBal)
	h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, spar).Scan(&sBal)
	if gBal != 1000+3*5000-2*1800-500 || sBal != 500 {
		t.Errorf("Salden Privat %v / Spar %v", gBal, sBal)
	}
	var marked int
	h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE recurring_id IS NOT NULL`).Scan(&marked)
	if marked != 6 {
		t.Errorf("%d Buchungen als Dauerauftrag markiert, want 6", marked)
	}
}

func TestUpdateTransactionAdjustsBalances(t *testing.T) {
	h := newDepotTestHandlers(t)
	var giro, spar int64
	h.db.QueryRow(`SELECT id FROM accounts WHERE name = 'Privatkonto'`).Scan(&giro)
	h.db.QueryRow(`SELECT id FROM accounts WHERE name = 'Sparkonto'`).Scan(&spar)

	if err := h.insertTransactionAndUpdateBalance(giro, sql.NullInt64{}, 1,
		transactionRecord{BookedAt: "2026-11-10", Kind: "expense", Amount: 100, Description: "Coop"}); err != nil {
		t.Fatal(err)
	}
	var id int64
	h.db.QueryRow(`SELECT MAX(id) FROM transactions`).Scan(&id)

	balances := func() (float64, float64) {
		var g, s float64
		h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, giro).Scan(&g)
		h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, spar).Scan(&s)
		return g, s
	}

	// Betrag korrigieren: −100 → −80
	f := txForm{AccountID: giro, Record: transactionRecord{Kind: "expense", Amount: 80, Description: "Coop"}}
	if err := h.updateTransaction(id, f, "2026-11-10"); err != nil {
		t.Fatal(err)
	}
	if g, s := balances(); g != -80 || s != 0 {
		t.Errorf("nach Betragsänderung: %v / %v, want -80 / 0", g, s)
	}

	// Aus der Ausgabe einen Transfer aufs Sparkonto machen.
	f = txForm{AccountID: giro, ToAccountID: sql.NullInt64{Int64: spar, Valid: true},
		Record: transactionRecord{Kind: "transfer", Amount: 200}}
	if err := h.updateTransaction(id, f, "2026-11-11"); err != nil {
		t.Fatal(err)
	}
	if g, s := balances(); g != -200 || s != 200 {
		t.Errorf("nach Umwandlung in Transfer: %v / %v, want -200 / 200", g, s)
	}

	// Konto wechseln: Einnahme aufs Sparkonto.
	f = txForm{AccountID: spar, Record: transactionRecord{Kind: "income", Amount: 50}}
	if err := h.updateTransaction(id, f, "2026-11-11"); err != nil {
		t.Fatal(err)
	}
	if g, s := balances(); g != 0 || s != 50 {
		t.Errorf("nach Kontowechsel: %v / %v, want 0 / 50", g, s)
	}

	if err := h.updateTransaction(9999, f, "2026-11-11"); err != sql.ErrNoRows {
		t.Errorf("unbekannte Buchung: err = %v, want sql.ErrNoRows", err)
	}
}
