package handlers

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"household-app/internal/bankimport"
)

func TestBuildImportRowsAndImport(t *testing.T) {
	h := newDepotTestHandlers(t)
	giro, spar := accountID(t, h, "Privatkonto"), accountID(t, h, "Sparkonto")
	mustExec(t, h, `INSERT INTO category_rules (keyword, category) VALUES ('migros', 'Lebensmittel')`)
	// Von Hand erfasst: dieselbe Zahlung, zwei Tage früher (Kartenkauf).
	if err := h.insertTransactionAndUpdateBalance(giro, sql.NullInt64{}, 1,
		transactionRecord{BookedAt: "2026-11-01", Kind: "expense", Amount: 42.5, Description: "Einkauf"}); err != nil {
		t.Fatal(err)
	}

	st := bankimport.Statement{From: "2026-11-01", To: "2026-11-05", Entries: []bankimport.Entry{
		{Date: "2026-11-03", Ref: "A/1", Type: "Karte", Payee: "Migros Beispiel", Amount: 42.5},
		{Date: "2026-11-04", Ref: "A/2", Type: "Lohn", Payee: "Arbeitgeber AG", Amount: 4000, Credit: true},
		{Date: "2026-11-05", Ref: "A/3", Type: "E-Banking", Payee: "Ich selbst", Amount: 1000},
	}}
	rows, err := h.buildImportRows(giro, st)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Duplicate == "" || rows[0].Selected || rows[0].Category != "Lebensmittel" {
		t.Errorf("Zeile 0: %+v, want Duplikat-Hinweis, abgewählt, Kategorie per Regel", rows[0])
	}
	if !rows[1].Selected || rows[1].Duplicate != "" {
		t.Errorf("Zeile 1: %+v, want ausgewählt", rows[1])
	}

	accounts, _ := h.loadAccounts()
	form := url.Values{}
	for _, r := range rows[1:] {
		i := strings.TrimPrefix(r.Hash, "wir:A/")
		form.Add("sel", i)
		form.Set("date_"+i, r.Entry.Date)
		form.Set("amount_"+i, "1000")
		form.Set("hash_"+i, r.Hash)
		form.Set("desc_"+i, r.Description)
	}
	form.Set("amount_2", "4000")
	form.Set("credit_2", "1")
	form.Set("cat_2", "Lohn")
	form.Set("other_3", strconv.FormatInt(spar, 10)) // Umbuchung aufs Sparkonto
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ParseForm()

	var lines []importLine
	for _, idx := range req.Form["sel"] {
		l, err := parseImportLine(req, idx, accounts, giro)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l)
	}
	n, skipped, err := h.importLines(1, lines)
	if err != nil || n != 2 || skipped != 0 {
		t.Fatalf("Import: %d, übersprungen %d, err %v", n, skipped, err)
	}
	// -42.50 (von Hand) + 4000 - 1000 Umbuchung
	if b := balance(h, giro); b != 2957.5 {
		t.Errorf("Privatkonto = %v, want 2957.5", b)
	}
	if b := balance(h, spar); b != 1000 {
		t.Errorf("Sparkonto = %v, want 1000", b)
	}
	var kind string
	h.db.QueryRow(`SELECT kind FROM transactions WHERE import_hash = 'wir:A/3'`).Scan(&kind)
	if kind != "transfer" {
		t.Errorf("A/3: kind %q, want transfer", kind)
	}

	// Zweiter Import derselben Zeilen: nichts doppelt.
	n, skipped, _ = h.importLines(1, lines)
	if n != 0 || skipped != 2 {
		t.Errorf("zweiter Import: %d importiert, %d übersprungen", n, skipped)
	}
	rows, _ = h.buildImportRows(giro, st)
	if !rows[1].Imported || rows[1].Selected {
		t.Errorf("nach Import: %+v, want als importiert markiert", rows[1])
	}
}
