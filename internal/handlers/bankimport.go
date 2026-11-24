package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household-app/internal/bankimport"
	"household-app/internal/models"
)

// maxStatementSize begrenzt hochgeladene Auszüge (ein WIR-PDF mit ein paar
// Monaten hat < 1 MB).
const maxStatementSize = 20 << 20

// importRow ist eine Zeile der Import-Vorschau.
type importRow struct {
	Index       int
	DateLabel   string // TT.MM.JJJJ
	Entry       bankimport.Entry
	Hash        string
	Description string
	Category    string
	RuleKeyword string // Vorschlag fürs Stichwort, wenn man eine Regel merken will
	FromRule    bool   // Kategorie kommt aus einer bestehenden Regel
	Imported    bool   // mit dieser Auftragsnummer schon importiert
	Duplicate   string // möglicherweise schon von Hand erfasst: Hinweis dazu
	Selected    bool
}

type importPageData struct {
	Accounts        []models.Account
	Categories      []models.Category
	AccountID       int64
	AccountName     string
	AccountBalance  float64
	Statement       *bankimport.Statement
	FromLabel       string
	ToLabel         string
	Rows            []importRow
	SelectedCount   int
	ImportedCount   int
	DuplicateCount  int
	ErrorMsg        string
	SuccessMsg      string
	Page            string
	CurrentUserName string
}

// ImportPage zeigt das Formular zum Hochladen eines Kontoauszugs.
func (h *Handlers) ImportPage(w http.ResponseWriter, r *http.Request) {
	data, err := h.importPageBase(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.ErrorMsg = r.URL.Query().Get("error")
	data.SuccessMsg = r.URL.Query().Get("success")
	if id, err := strconv.ParseInt(r.URL.Query().Get("account"), 10, 64); err == nil {
		data.AccountID = id
	}
	h.renderImport(w, data)
}

func (h *Handlers) importPageBase(r *http.Request) (importPageData, error) {
	accounts, err := h.loadAccounts()
	if err != nil {
		return importPageData{}, err
	}
	categories, err := h.loadCategories()
	if err != nil {
		return importPageData{}, err
	}
	data := importPageData{
		Accounts:        accounts,
		Categories:      categories,
		Page:            "import",
		CurrentUserName: currentUser(r).Name,
	}
	// Vorauswahl: das erste Konto mit „WIR" im Namen, sonst das erste Girokonto.
	for _, a := range accounts {
		if strings.Contains(strings.ToLower(a.Name), "wir") {
			data.AccountID = a.ID
			break
		}
	}
	if data.AccountID == 0 {
		for _, a := range accounts {
			if a.Type == "Girokonto" {
				data.AccountID = a.ID
				break
			}
		}
	}
	return data, nil
}

func (h *Handlers) renderImport(w http.ResponseWriter, data importPageData) {
	if err := h.tmpl.ExecuteTemplate(w, "import.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// PreviewImport liest den hochgeladenen Auszug und zeigt die Vorschau.
func (h *Handlers) PreviewImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxStatementSize)
	file, _, err := r.FormFile("file")
	if err != nil {
		redirectWithMessage(w, r, "/import", "error", "Bitte eine PDF-Datei auswählen (höchstens 20 MB).")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		redirectWithMessage(w, r, "/import", "error", "Datei konnte nicht gelesen werden.")
		return
	}

	data, err := h.importPageBase(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accountID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	account, ok := findAccount(data.Accounts, accountID)
	if !ok {
		redirectWithMessage(w, r, "/import", "error", "Bitte das Konto wählen, zu dem der Auszug gehört.")
		return
	}
	data.AccountID, data.AccountName, data.AccountBalance = account.ID, account.Name, account.Balance

	st, err := bankimport.ParseWIRPDF(raw)
	if err != nil {
		redirectWithMessage(w, r, fmt.Sprintf("/import?account=%d", accountID), "error", err.Error())
		return
	}
	data.Statement = &st
	data.FromLabel, data.ToLabel = germanDate(st.From), germanDate(st.To)

	rows, err := h.buildImportRows(account.ID, st)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Rows = rows
	for _, row := range rows {
		switch {
		case row.Imported:
			data.ImportedCount++
		case row.Duplicate != "":
			data.DuplicateCount++
		}
		if row.Selected {
			data.SelectedCount++
		}
	}
	h.renderImport(w, data)
}

func findAccount(accounts []models.Account, id int64) (models.Account, bool) {
	for _, a := range accounts {
		if a.ID == id {
			return a, true
		}
	}
	return models.Account{}, false
}

func importHash(e bankimport.Entry) string {
	return "wir:" + e.Ref
}

// buildImportRows bereitet die Buchungen für die Vorschau auf: Kategorie per
// Regel, schon importierte und möglicherweise von Hand erfasste erkennen.
func (h *Handlers) buildImportRows(accountID int64, st bankimport.Statement) ([]importRow, error) {
	rules, err := h.loadRules()
	if err != nil {
		return nil, err
	}

	imported := map[string]bool{}
	hashRows, err := h.db.Query(`SELECT import_hash FROM transactions WHERE import_hash LIKE 'wir:%'`)
	if err != nil {
		return nil, err
	}
	for hashRows.Next() {
		var hash string
		if err := hashRows.Scan(&hash); err != nil {
			hashRows.Close()
			return nil, err
		}
		imported[hash] = true
	}
	hashRows.Close()

	candidates, err := h.manualCandidates(accountID, st.From, st.To)
	if err != nil {
		return nil, err
	}

	var rows []importRow
	for i, e := range st.Entries {
		row := importRow{
			Index:       i,
			DateLabel:   germanDate(e.Date),
			Entry:       e,
			Hash:        importHash(e),
			Description: e.Description(),
			Category:    matchRule(rules, e.Payee+" "+e.RawText),
			RuleKeyword: suggestKeyword(e.Payee),
		}
		row.FromRule = row.Category != ""
		row.Imported = imported[row.Hash]
		if !row.Imported {
			if c := matchCandidate(candidates, e); c != nil {
				row.Duplicate = fmt.Sprintf("schon erfasst? %s · %s", germanDate(c.date), c.label)
			}
		}
		row.Selected = !row.Imported && row.Duplicate == ""
		rows = append(rows, row)
	}
	return rows, nil
}

// candidate ist eine von Hand (nicht per Import) erfasste Buchung, die zu
// einer Zeile des Auszugs passen könnte.
type candidate struct {
	date   string
	amount float64
	credit bool // Geld kam aufs Konto
	label  string
	used   bool
}

func (h *Handlers) manualCandidates(accountID int64, from, to string) ([]*candidate, error) {
	rows, err := h.db.Query(
		`SELECT booked_at, amount, kind, account_id, COALESCE(description, ''), COALESCE(category, '')
		 FROM transactions
		 WHERE (account_id = ? OR to_account_id = ?) AND import_hash IS NULL
		   AND booked_at BETWEEN date(?, '-7 days') AND date(?, '+7 days')
		 ORDER BY booked_at`, accountID, accountID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*candidate
	for rows.Next() {
		var c candidate
		var kind, desc, cat string
		var acc int64
		if err := rows.Scan(&c.date, &c.amount, &kind, &acc, &desc, &cat); err != nil {
			return nil, err
		}
		c.credit = kind == "income" || (kind == "transfer" && acc != accountID)
		c.label = desc
		if c.label == "" {
			c.label = cat
		}
		if c.label == "" {
			c.label = FormatCHF(c.amount)
		}
		list = append(list, &c)
	}
	return list, rows.Err()
}

// matchCandidate sucht eine noch nicht zugeordnete Buchung mit gleichem
// Betrag und gleicher Richtung, höchstens 5 Tage auseinander (Kartenzahlungen
// bucht die Bank oft erst 2–3 Tage später).
func matchCandidate(list []*candidate, e bankimport.Entry) *candidate {
	var best *candidate
	bestDiff := 6.0
	for _, c := range list {
		if c.used || c.credit != e.Credit || math.Abs(c.amount-e.Amount) > 0.005 {
			continue
		}
		if diff := dayDiff(c.date, e.Date); diff < bestDiff {
			best, bestDiff = c, diff
		}
	}
	if best != nil {
		best.used = true
	}
	return best
}

func dayDiff(a, b string) float64 {
	ta, errA := time.Parse("2006-01-02", a)
	tb, errB := time.Parse("2006-01-02", b)
	if errA != nil || errB != nil {
		return math.Inf(1)
	}
	return math.Abs(ta.Sub(tb).Hours() / 24)
}

// ConfirmImport bucht die in der Vorschau ausgewählten Zeilen.
func (h *Handlers) ConfirmImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accountID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	account, ok := findAccount(accounts, accountID)
	if !ok {
		redirectWithMessage(w, r, "/import", "error", "Konto nicht gefunden.")
		return
	}

	var lines []importLine
	for _, idx := range r.Form["sel"] {
		line, err := parseImportLine(r, idx, accounts, account.ID)
		if err != nil {
			redirectWithMessage(w, r, "/import", "error", err.Error())
			return
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		redirectWithMessage(w, r, "/import", "error", "Keine Buchung ausgewählt.")
		return
	}

	n, skipped, err := h.importLines(currentUser(r).ID, lines)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rulesAdded, rulesSkipped, categorized, err := h.rulesFromImport(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var balance float64
	h.db.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, account.ID).Scan(&balance)
	msg := fmt.Sprintf("%d Buchung(en) auf «%s» importiert. Saldo in der App jetzt %s.", n, account.Name, FormatCHF(balance))
	if bank := r.FormValue("bank_balance"); bank != "" {
		if b, err := strconv.ParseFloat(bank, 64); err == nil {
			if math.Abs(b-balance) < 0.005 {
				msg += " Stimmt mit dem Auszug überein."
			} else {
				msg += fmt.Sprintf(" Laut Auszug %s – abgleichen in den Einstellungen unter Konten.", FormatCHF(b))
			}
		}
	}
	if rulesAdded > 0 {
		msg += fmt.Sprintf(" %d Regel(n) gemerkt", rulesAdded)
		if categorized > 0 {
			msg += fmt.Sprintf(", %d weitere Buchung(en) damit zugeordnet", categorized)
		}
		msg += "."
	}
	if rulesSkipped > 0 {
		msg += fmt.Sprintf(" %d Regel(n) gab es schon.", rulesSkipped)
	}
	if skipped > 0 {
		msg += fmt.Sprintf(" %d waren schon importiert und wurden übersprungen.", skipped)
	}
	redirectWithMessage(w, r, "/", "success", msg)
}

type importLine struct {
	accountID   int64
	toAccountID sql.NullInt64
	rec         transactionRecord
	hash        string
}

// parseImportLine liest eine ausgewählte Zeile aus dem Vorschau-Formular.
func parseImportLine(r *http.Request, idx string, accounts []models.Account, accountID int64) (importLine, error) {
	field := func(name string) string { return strings.TrimSpace(r.FormValue(name + "_" + idx)) }
	date := field("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return importLine{}, fmt.Errorf("Zeile %s: ungültiges Datum", idx)
	}
	amount, err := strconv.ParseFloat(field("amount"), 64)
	if err != nil || amount <= 0 {
		return importLine{}, fmt.Errorf("Zeile %s: ungültiger Betrag", idx)
	}
	hash := field("hash")
	if !strings.HasPrefix(hash, "wir:") {
		return importLine{}, fmt.Errorf("Zeile %s: Auftragsnummer fehlt", idx)
	}
	credit := field("credit") == "1"

	line := importLine{
		accountID: accountID,
		hash:      hash,
		rec: transactionRecord{
			BookedAt:    date,
			Amount:      amount,
			Description: field("desc"),
			Category:    field("cat"),
			Kind:        "expense",
		},
	}
	if credit {
		line.rec.Kind = "income"
	}
	// Gegenkonto gewählt: Umbuchung statt Einnahme/Ausgabe.
	if other, _ := strconv.ParseInt(field("other"), 10, 64); other != 0 {
		if _, ok := findAccount(accounts, other); !ok || other == accountID {
			return importLine{}, fmt.Errorf("Zeile %s: ungültiges Gegenkonto", idx)
		}
		line.rec.Kind = "transfer"
		line.rec.Category = ""
		if credit {
			line.accountID, line.toAccountID = other, sql.NullInt64{Int64: accountID, Valid: true}
		} else {
			line.toAccountID = sql.NullInt64{Int64: other, Valid: true}
		}
	}
	return line, nil
}

// importLines bucht alle Zeilen in einer Datenbank-Transaktion. Zeilen, deren
// Auftragsnummer schon importiert ist, werden übersprungen.
func (h *Handlers) importLines(userID int64, lines []importLine) (imported, skipped int, err error) {
	tx, err := h.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	for _, l := range lines {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM transactions WHERE import_hash = ?`, l.hash).Scan(&exists); err != nil {
			return 0, 0, err
		}
		if exists > 0 {
			skipped++
			continue
		}
		if l.rec.Category != "" {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO categories (name) VALUES (?)`, l.rec.Category); err != nil {
				return 0, 0, err
			}
		}
		if err := insertTransactionTx(tx, l.accountID, l.toAccountID, userID, l.rec, l.hash); err != nil {
			return 0, 0, err
		}
		imported++
	}
	return imported, skipped, tx.Commit()
}

// rulesFromImport legt die in der Vorschau angehakten Regeln an („Regel
// merken"). Läuft nach dem Import, damit die neuen Regeln auch importierte
// Buchungen ohne Kategorie gleich zuordnen.
func (h *Handlers) rulesFromImport(r *http.Request) (added, existed int, categorized int64, err error) {
	seen := map[string]bool{}
	for key := range r.Form {
		idx, ok := strings.CutPrefix(key, "rule_")
		if !ok || r.FormValue(key) != "1" || r.FormValue("other_"+idx) != "" && r.FormValue("other_"+idx) != "0" {
			continue
		}
		keyword := strings.TrimSpace(r.FormValue("kw_" + idx))
		category := strings.TrimSpace(r.FormValue("cat_" + idx))
		if keyword == "" || category == "" || seen[strings.ToLower(keyword)] {
			continue
		}
		seen[strings.ToLower(keyword)] = true
		n, err := h.addRule(keyword, category)
		if err == errRuleExists {
			existed++
			continue
		}
		if err != nil {
			return 0, 0, 0, err
		}
		added++
		categorized += n
	}
	return added, existed, categorized, nil
}

// suggestKeyword schlägt aus dem Händler ein Stichwort für eine Regel vor:
// das erste aussagekräftige Wort, z. B. "Coop-2899 Oftringen" → "Coop",
// "0143 - Lidl Rothrist" → "Lidl", "MC DONALDS 00851" → "MC DONALDS".
func suggestKeyword(payee string) string {
	var words []string
	for _, f := range strings.FieldsFunc(payee, func(r rune) bool {
		return r == ' ' || r == '-' || r == '*' || r == ',' || r == '/'
	}) {
		if strings.Trim(f, "0123456789.") == "" {
			continue // reine Nummern (Filialnummer)
		}
		words = append(words, f)
		if len([]rune(strings.Join(words, " "))) >= 3 {
			break
		}
	}
	return strings.Join(words, " ")
}
