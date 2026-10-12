package handlers

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household-app/internal/models"
	"household-app/internal/stocks"
)

const (
	// Sparplan-Käufe werden als Transfer auf dieses Konto gebucht (Einstandswert),
	// damit das Vermögen nicht sinkt und der Kauf nicht als Ausgabe zählt.
	investmentAccountName = "Yuh Anlagen"
	investmentAccountType = "Depot"

	maxImportBytes = 10 << 20
)

// yuhRow ist eine ausgewertete Zeile des Yuh-Aktivitätenexports.
type yuhRow struct {
	Line         int
	Date         string // ISO (2006-01-02)
	ActivityType string
	Name         string
	Debit        float64
	HasDebit     bool
	DebitCur     string
	Credit       float64
	HasCredit    bool
	CreditCur    string
	CardNumber   string
	Locality     string
	Recipient    string
	Sender       string
	Fees         float64
	BuySell      string // "BUY", "SELL" oder leer
	Quantity     float64
	Asset        string
	Price        float64
}

type yuhKind int

const (
	yuhIncome yuhKind = iota
	yuhExpense
	yuhBuy
	yuhSkip
)

// yuhAction beschreibt, was mit einer Zeile beim Import geschieht.
type yuhAction struct {
	Kind   yuhKind
	Amount float64 // immer positiv, auf Rappen gerundet
	Reason string  // nur bei yuhSkip
}

// detectDelimiter erkennt anhand der Kopfzeile, ob Semikolon, Komma oder
// Tabulator getrennt wird (je nach Export bzw. Numbers/Excel-Zwischenschritt).
func detectDelimiter(headerLine string) rune {
	best := ','
	bestCount := strings.Count(headerLine, ",")
	if c := strings.Count(headerLine, ";"); c > bestCount {
		best, bestCount = ';', c
	}
	if c := strings.Count(headerLine, "\t"); c > bestCount {
		best = '\t'
	}
	return best
}

// parseYuhNumber liest eine Zahl in gängigen Schreibweisen ("-24.55",
// "24,55", "1'234.50", "1,234.50", "1.234,50"). Leer ergibt ok=false.
func parseYuhNumber(s string) (value float64, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, nil
	}
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")

	hasComma := strings.Contains(s, ",")
	hasDot := strings.Contains(s, ".")
	switch {
	case hasComma && hasDot:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			// "1.234,50": Punkt = Tausender, Komma = Dezimal
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			// "1,234.50": Komma = Tausender
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasComma:
		s = strings.ReplaceAll(s, ",", ".")
	}

	v, perr := strconv.ParseFloat(s, 64)
	if perr != nil {
		return 0, false, perr
	}
	return v, true, nil
}

// parseYuhDate liest ein Datum als Tag/Monat/Jahr ("11/04/2025", "11.04.2025")
// oder ISO ("2025-04-11") und gibt es als ISO zurück. Eine angehängte Uhrzeit
// wird ignoriert.
func parseYuhDate(s string) (string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", fmt.Errorf("Datum fehlt")
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "2/1/2006", "02.01.2006", "2.1.2006"} {
		if t, err := time.Parse(layout, fields[0]); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("Datum %q nicht lesbar", s)
}

func isBlankRecord(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// parseYuhCSV liest den Yuh-Aktivitätenexport. Zeilen mit unlesbaren Werten
// werden übersprungen und als Hinweis zurückgegeben; fehlende Pflichtspalten
// oder eine leere Datei sind ein Fehler.
func parseYuhCSV(data []byte) (rows []yuhRow, problems []string, err error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, nil, fmt.Errorf("die Datei ist leer")
	}

	firstLine := text
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		firstLine = text[:i]
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = detectDelimiter(firstLine)
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	records, readErr := reader.ReadAll()
	if readErr != nil {
		return nil, nil, fmt.Errorf("CSV konnte nicht gelesen werden: %v", readErr)
	}
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("die Datei enthält keine Datenzeilen")
	}

	idx := map[string]int{}
	for i, name := range records[0] {
		idx[strings.ToUpper(strings.TrimSpace(name))] = i
	}
	var missing []string
	for _, col := range []string{"DATE", "ACTIVITY TYPE", "ACTIVITY NAME", "DEBIT", "CREDIT"} {
		if _, ok := idx[col]; !ok {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("Spalte(n) fehlen: %s (ist das ein Yuh-Aktivitätenexport?)", strings.Join(missing, ", "))
	}

	get := func(rec []string, col string) string {
		i, ok := idx[col]
		if !ok || i >= len(rec) {
			return ""
		}
		// Numbers/Excel verdoppeln Anführungszeichen teils doppelt ("""Lidl"""),
		// übrig bleiben dann Anführungszeichen um den Wert.
		return strings.TrimSpace(strings.Trim(strings.TrimSpace(rec[i]), `"`))
	}

	for i, rec := range records[1:] {
		line := i + 2
		if isBlankRecord(rec) {
			continue
		}

		row := yuhRow{
			Line:         line,
			ActivityType: get(rec, "ACTIVITY TYPE"),
			Name:         get(rec, "ACTIVITY NAME"),
			DebitCur:     get(rec, "DEBIT CURRENCY"),
			CreditCur:    get(rec, "CREDIT CURRENCY"),
			CardNumber:   get(rec, "CARD NUMBER"),
			Locality:     get(rec, "LOCALITY"),
			Recipient:    get(rec, "RECIPIENT"),
			Sender:       get(rec, "SENDER"),
			BuySell:      strings.ToUpper(get(rec, "BUY/SELL")),
			Asset:        get(rec, "ASSET"),
		}

		date, dateErr := parseYuhDate(get(rec, "DATE"))
		if dateErr != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: %v", line, dateErr))
			continue
		}
		row.Date = date

		numbers := []struct {
			col string
			val *float64
			has *bool
		}{
			{"DEBIT", &row.Debit, &row.HasDebit},
			{"CREDIT", &row.Credit, &row.HasCredit},
			{"FEES/COMMISSION", &row.Fees, new(bool)},
			{"QUANTITY", &row.Quantity, new(bool)},
			{"PRICE PER UNIT", &row.Price, new(bool)},
		}
		bad := false
		for _, n := range numbers {
			v, has, numErr := parseYuhNumber(get(rec, n.col))
			if numErr != nil {
				problems = append(problems, fmt.Sprintf("Zeile %d: %s %q ist keine Zahl", line, n.col, get(rec, n.col)))
				bad = true
				break
			}
			*n.val = v
			*n.has = has
		}
		if bad {
			continue
		}

		rows = append(rows, row)
	}

	return rows, problems, nil
}

func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// classifyYuhRow entscheidet, ob eine Zeile als Einnahme, Ausgabe oder
// Depot-Kauf gebucht oder übersprungen wird.
func classifyYuhRow(r yuhRow) yuhAction {
	for _, cur := range []string{r.DebitCur, r.CreditCur} {
		if cur != "" && !strings.EqualFold(cur, "CHF") {
			return yuhAction{Kind: yuhSkip, Reason: fmt.Sprintf("Fremdwährung %s wird nicht umgerechnet", strings.ToUpper(cur))}
		}
	}

	switch r.BuySell {
	case "BUY":
		if r.Asset == "" || r.Quantity <= 0 {
			return yuhAction{Kind: yuhSkip, Reason: "Kauf ohne Asset oder Menge"}
		}
		amount := math.Abs(r.Debit)
		if !r.HasDebit || amount == 0 {
			amount = r.Quantity*r.Price + math.Abs(r.Fees)
		}
		amount = roundCents(amount)
		if amount <= 0 {
			return yuhAction{Kind: yuhSkip, Reason: "Kauf ohne Betrag"}
		}
		return yuhAction{Kind: yuhBuy, Amount: amount}
	case "SELL":
		return yuhAction{Kind: yuhSkip, Reason: fmt.Sprintf("Verkauf %s bitte von Hand im Depot anpassen", r.Asset)}
	}

	net := 0.0
	if r.HasCredit {
		net += math.Abs(r.Credit)
	}
	if r.HasDebit {
		net -= math.Abs(r.Debit)
	}
	net = roundCents(net)

	switch {
	case net > 0:
		return yuhAction{Kind: yuhIncome, Amount: net}
	case net < 0:
		return yuhAction{Kind: yuhExpense, Amount: -net}
	default:
		return yuhAction{Kind: yuhSkip, Reason: "Betrag fehlt oder ist 0"}
	}
}

// yuhDescription baut die Buchungsbeschreibung (Händler und Ort).
func yuhDescription(r yuhRow) string {
	name := strings.Trim(strings.TrimSpace(r.Name), `"`)
	if name == "" {
		for _, c := range []string{r.Recipient, r.Sender, r.ActivityType} {
			if c != "" {
				name = c
				break
			}
		}
	}
	if r.Locality != "" {
		name += " (" + r.Locality + ")"
	}
	return name
}

func yuhPurchaseDescription(r yuhRow) string {
	return fmt.Sprintf("Kauf %s (%s Stk.)", r.Asset, strconv.FormatFloat(r.Quantity, 'f', -1, 64))
}

// categorizeYuh wendet zuerst die Regeln des Nutzers an und fällt dann auf
// wenige feste Zuordnungen nach Aktivitätstyp zurück.
func categorizeYuh(rules []models.CategoryRule, r yuhRow) string {
	if c := matchRule(rules, r.Name+" "+r.Recipient+" "+r.Sender); c != "" {
		return c
	}
	t := strings.ToUpper(r.ActivityType)
	switch {
	case strings.Contains(t, "REWARD"):
		return "Bonus"
	case strings.Contains(t, "DIVIDEND"):
		return "Dividenden"
	case strings.Contains(t, "INTEREST"):
		return "Zinsen"
	}
	return ""
}

// yuhRowKey fasst alle relevanten Felder einer Zeile zusammen. Zusammen mit
// der Nummer des Vorkommens ergibt das den Fingerabdruck für die
// Duplikaterkennung.
func yuhRowKey(r yuhRow) string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 8, 64) }
	return strings.Join([]string{
		r.Date, r.ActivityType, r.Name, f(r.Debit), f(r.Credit),
		r.DebitCur, r.CreditCur, r.CardNumber, r.Locality, r.Recipient, r.Sender,
		r.BuySell, r.Asset, f(r.Quantity), f(r.Price), f(r.Fees),
	}, "|")
}

// yuhHash berücksichtigt, das wievielte Vorkommen derselben Zeile es ist:
// zwei identische Zahlungen am selben Tag bleiben so beide erhalten, ein
// erneuter Import derselben Datei erzeugt trotzdem keine Duplikate.
func yuhHash(key string, occurrence int) string {
	sum := sha256.Sum256([]byte(key + "#" + strconv.Itoa(occurrence)))
	return "yuh:" + hex.EncodeToString(sum[:])
}

type tickerInfo struct {
	Ticker   string
	Verified bool
}

// resolveTicker versucht zuerst die SIX-Notierung ("VWRL" -> "VWRL.SW") und
// dann das Kürzel unverändert. Ist keiner abrufbar, wird die SIX-Variante
// ungeprüft verwendet.
func resolveTicker(asset string) tickerInfo {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	candidates := []string{asset}
	if !strings.Contains(asset, ".") {
		candidates = []string{asset + ".SW", asset}
	}
	for _, c := range candidates {
		if _, err := stocks.GetQuote(c); err == nil {
			return tickerInfo{Ticker: c, Verified: true}
		}
	}
	return tickerInfo{Ticker: candidates[0], Verified: false}
}

type yuhResult struct {
	Imported   int
	Purchases  int
	Duplicates int
	Problems   []string
}

// yuhSummary baut die Rückmeldung für die Einstellungsseite.
func yuhSummary(res yuhResult, problems []string) (kind, message string) {
	message = fmt.Sprintf("%d Buchung(en) importiert", res.Imported)
	if res.Purchases > 0 {
		message += fmt.Sprintf(" (davon %d Depot-Kauf/-Käufe)", res.Purchases)
	}
	message += "."
	if res.Duplicates > 0 {
		message += fmt.Sprintf(" %d bereits vorhandene übersprungen.", res.Duplicates)
	}
	if len(problems) == 0 {
		return "success", message
	}

	shown := problems
	if len(problems) > 5 {
		shown = append(append([]string{}, problems[:5]...), fmt.Sprintf("… und %d weitere", len(problems)-5))
	}
	return "error", message + " Hinweise: " + strings.Join(shown, " | ")
}

// ImportYuh importiert einen Yuh-Aktivitätenexport (CSV) auf das gewählte Konto.
func (h *Handlers) ImportYuh(w http.ResponseWriter, r *http.Request) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Bitte ein Konto für den Import auswählen.")
		return
	}

	var accountCount int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, accountID).Scan(&accountCount); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if accountCount == 0 {
		redirectWithMessage(w, r, "/settings", "error", "Das gewählte Konto wurde nicht gefunden.")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Bitte eine CSV-Datei auswählen.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, problems, err := parseYuhCSV(data)
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Yuh-Import: "+err.Error())
		return
	}

	rules, err := h.loadRules()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := h.importYuhRows(rows, accountID, currentUser(r).ID, rules)
	problems = append(problems, result.Problems...)

	kind, message := yuhSummary(result, problems)
	redirectWithMessage(w, r, "/settings", kind, message)
}

// importYuhRows bucht die Zeilen einzeln (jede in ihrer eigenen
// Datenbank-Transaktion). Eine fehlerhafte Zeile bricht den Rest nicht ab.
func (h *Handlers) importYuhRows(rows []yuhRow, accountID, userID int64, rules []models.CategoryRule) yuhResult {
	var res yuhResult
	occurrences := map[string]int{}
	tickers := map[string]tickerInfo{}
	var investAccountID int64

	for _, row := range rows {
		action := classifyYuhRow(row)
		if action.Kind == yuhSkip {
			res.Problems = append(res.Problems, fmt.Sprintf("Zeile %d: %s", row.Line, action.Reason))
			continue
		}

		key := yuhRowKey(row)
		occurrences[key]++
		hash := yuhHash(key, occurrences[key])

		var existing int
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE import_hash = ?`, hash).Scan(&existing); err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("Zeile %d: %v", row.Line, err))
			continue
		}
		if existing > 0 {
			res.Duplicates++
			continue
		}

		switch action.Kind {
		case yuhIncome, yuhExpense:
			kind := "income"
			if action.Kind == yuhExpense {
				kind = "expense"
			}
			rec := transactionRecord{
				BookedAt:    row.Date,
				Kind:        kind,
				Amount:      action.Amount,
				Category:    categorizeYuh(rules, row),
				Description: yuhDescription(row),
			}
			if err := h.bookImported(accountID, sql.NullInt64{}, userID, rec, hash, nil); err != nil {
				res.Problems = append(res.Problems, fmt.Sprintf("Zeile %d: %v", row.Line, err))
				continue
			}
			res.Imported++

		case yuhBuy:
			if investAccountID == 0 {
				id, err := h.ensureAccount(investmentAccountName, investmentAccountType)
				if err != nil {
					res.Problems = append(res.Problems, fmt.Sprintf("Zeile %d: %v", row.Line, err))
					continue
				}
				investAccountID = id
			}

			assetKey := strings.ToUpper(row.Asset)
			info, known := tickers[assetKey]
			if !known {
				info = resolveTicker(row.Asset)
				tickers[assetKey] = info
				if !info.Verified {
					res.Problems = append(res.Problems, fmt.Sprintf(
						"Kurs für %s nicht abrufbar, als %q gespeichert (bitte im Depot prüfen)", row.Asset, info.Ticker))
				}
			}

			price := row.Price
			if price <= 0 {
				price = (action.Amount - math.Abs(row.Fees)) / row.Quantity
			}
			if price <= 0 {
				price = action.Amount / row.Quantity
			}

			rec := transactionRecord{
				BookedAt:    row.Date,
				Kind:        "transfer",
				Amount:      action.Amount,
				Description: yuhPurchaseDescription(row),
			}
			holding := &models.Holding{
				Ticker:        info.Ticker,
				Quantity:      row.Quantity,
				PurchasePrice: price,
				PurchaseDate:  row.Date,
			}
			toAccount := sql.NullInt64{Int64: investAccountID, Valid: true}
			if err := h.bookImported(accountID, toAccount, userID, rec, hash, holding); err != nil {
				res.Problems = append(res.Problems, fmt.Sprintf("Zeile %d: %v", row.Line, err))
				continue
			}
			res.Imported++
			res.Purchases++
		}
	}

	return res
}

// bookImported schreibt eine importierte Buchung (und bei Käufen die
// Depot-Position) atomar in einer Datenbank-Transaktion.
func (h *Handlers) bookImported(accountID int64, toAccountID sql.NullInt64, userID int64, rec transactionRecord, hash string, holding *models.Holding) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := insertTransactionTx(tx, accountID, toAccountID, userID, rec, hash); err != nil {
		return err
	}

	if holding != nil {
		if _, err := tx.Exec(
			`INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date) VALUES (?, ?, ?, ?)`,
			holding.Ticker, holding.Quantity, holding.PurchasePrice, holding.PurchaseDate,
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ensureAccount liefert die ID eines Kontos mit diesem Namen und legt es bei
// Bedarf mit Saldo 0 an.
func (h *Handlers) ensureAccount(name, accountType string) (int64, error) {
	var id int64
	err := h.db.QueryRow(`SELECT id FROM accounts WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	res, err := h.db.Exec(`INSERT INTO accounts (name, type, balance) VALUES (?, ?, 0)`, name, accountType)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
