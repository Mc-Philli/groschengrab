package handlers

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestSuggestKeyword(t *testing.T) {
	for in, want := range map[string]string{
		"Coop-2899 Oftringen Perry Center": "Coop",
		"0143 - Lidl Rothrist":             "Lidl",
		"MC DONALDS 00851":                 "MC DONALDS",
		"AMAZON* BB12A7CN4":                "AMAZON",
		"Beispiel, Anna":                   "Beispiel",
		"":                                 "",
	} {
		if got := suggestKeyword(in); got != want {
			t.Errorf("suggestKeyword(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRulesFromImport(t *testing.T) {
	h := newDepotTestHandlers(t)
	giro := accountID(t, h, "Privatkonto")
	mustExec(t, h, `INSERT INTO category_rules (keyword, category) VALUES ('migros', 'Lebensmittel')`)
	// Importierte Buchung ohne Kategorie: bekommt sie über die neue Regel.
	if err := h.insertTransactionAndUpdateBalance(giro, sql.NullInt64{}, 1,
		transactionRecord{BookedAt: "2026-11-02", Kind: "expense", Amount: 12, Description: "Denner Zofingen"}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"rule_1": {"1"}, "kw_1": {"Denner"}, "cat_1": {"Lebensmittel"},
		"rule_2": {"1"}, "kw_2": {"Migros"}, "cat_2": {"Lebensmittel"}, // gibt es schon
		"rule_3": {"1"}, "kw_3": {"Ich"}, "cat_3": {"Sparen"}, "other_3": {"2"}, // Umbuchung: keine Regel
		"rule_4": {"1"}, "kw_4": {"denner"}, "cat_4": {"Lebensmittel"}, // doppelt in derselben Vorschau
		"kw_5": {"SBB"}, "cat_5": {"Verkehr"}, // nicht angehakt
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ParseForm()

	added, existed, categorized, err := h.rulesFromImport(req)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || existed != 1 || categorized != 1 {
		t.Errorf("added %d, existed %d, categorized %d; want 1, 1, 1", added, existed, categorized)
	}
	var n int
	h.db.QueryRow(`SELECT COUNT(*) FROM category_rules`).Scan(&n)
	if n != 2 {
		t.Errorf("%d Regeln, want 2", n)
	}
	var cat string
	h.db.QueryRow(`SELECT category FROM transactions WHERE description = 'Denner Zofingen'`).Scan(&cat)
	if cat != "Lebensmittel" {
		t.Errorf("Kategorie %q, want Lebensmittel", cat)
	}
}

func TestReadBackupStatus(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.Local)
	if st := readBackupStatus(dir, now); st.Found {
		t.Errorf("ohne Datei: %+v", st)
	}
	os.WriteFile(filepath.Join(dir, "status"), []byte("time=2026-11-24T03:05:00\nfile=app-x.db.gz\nsize=1.2M\ncopy=USB: ok; Cloud: ok\n"), 0o644)
	st := readBackupStatus(dir, now)
	if !st.Found || st.Stale || !st.CopyOK || st.When != "24.11.2026 03:05" || st.Size != "1.2M" {
		t.Errorf("frisch: %+v", st)
	}
	os.WriteFile(filepath.Join(dir, "status"), []byte("time=2026-11-20T03:05:00\ncopy=USB: Ziel /mnt/usb nicht bereit\n"), 0o644)
	st = readBackupStatus(dir, now)
	if !st.Stale || st.CopyOK {
		t.Errorf("alt + Fehler: %+v", st)
	}
}
