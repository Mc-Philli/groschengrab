package handlers

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// utf8BOM verbessert die Darstellung von Umlauten, wenn die CSV-Datei direkt
// in Excel geöffnet wird.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ExportAccounts liefert alle Konten als CSV-Download.
func (h *Handlers) ExportAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="konten.csv"`)
	w.Write(utf8BOM)

	writer := csv.NewWriter(w)
	writer.Write([]string{"name", "type", "balance"})
	for _, a := range accounts {
		writer.Write([]string{a.Name, a.Type, strconv.FormatFloat(a.Balance, 'f', 2, 64)})
	}
	writer.Flush()
}

// ExportTransactions liefert alle Buchungen als CSV-Download. Konten und
// Nutzer werden als Namen (nicht IDs) exportiert, damit die Datei auch von
// Hand lesbar/bearbeitbar ist und sich unverändert wieder importieren lässt.
func (h *Handlers) ExportTransactions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT t.booked_at, t.kind, a.name, COALESCE(b.name, ''), t.amount,
		       COALESCE(t.category, ''), COALESCE(t.description, ''), u.name
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN accounts b ON b.id = t.to_account_id
		JOIN users u ON u.id = t.user_id
		ORDER BY t.booked_at, t.id`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="buchungen.csv"`)
	w.Write(utf8BOM)

	writer := csv.NewWriter(w)
	writer.Write([]string{"booked_at", "kind", "account", "to_account", "amount", "category", "description", "booked_by"})

	for rows.Next() {
		var bookedAt, kind, account, toAccount, category, description, bookedBy string
		var amount float64
		if err := rows.Scan(&bookedAt, &kind, &account, &toAccount, &amount, &category, &description, &bookedBy); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writer.Write([]string{
			bookedAt, kind, account, toAccount,
			strconv.FormatFloat(amount, 'f', 2, 64), category, description, bookedBy,
		})
	}
	writer.Flush()
}

// ImportAccounts liest eine hochgeladene CSV-Datei (Format wie ExportAccounts)
// und legt daraus neue Konten an.
func (h *Handlers) ImportAccounts(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Bitte eine CSV-Datei auswählen.")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "CSV konnte nicht gelesen werden: "+err.Error())
		return
	}
	if len(rows) < 2 {
		redirectWithMessage(w, r, "/settings", "error", "Die CSV-Datei enthält keine Daten (nur Kopfzeile oder leer).")
		return
	}

	imported := 0
	var problems []string

	for i, row := range rows[1:] {
		lineNum := i + 2
		if len(row) < 3 {
			problems = append(problems, fmt.Sprintf("Zeile %d: erwartet 3 Spalten (name,type,balance)", lineNum))
			continue
		}
		name := strings.TrimSpace(row[0])
		accType := strings.TrimSpace(row[1])
		balance, err := parseAmount(row[2])
		if name == "" {
			problems = append(problems, fmt.Sprintf("Zeile %d: Name fehlt", lineNum))
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: Saldo %q ungültig", lineNum, row[2]))
			continue
		}
		if _, err := h.db.Exec(`INSERT INTO accounts (name, type, balance) VALUES (?, ?, ?)`, name, accType, balance); err != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: %v", lineNum, err))
			continue
		}
		imported++
	}

	finishImport(w, r, "/settings", imported, "Konto(s)", problems)
}

// ImportTransactions liest eine hochgeladene CSV-Datei (Format wie
// ExportTransactions) und bucht daraus neue Buchungen inkl. Saldo-Update.
// Konten werden per Name aufgelöst und müssen bereits existieren.
func (h *Handlers) ImportTransactions(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Bitte eine CSV-Datei auswählen.")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "CSV konnte nicht gelesen werden: "+err.Error())
		return
	}
	if len(rows) < 2 {
		redirectWithMessage(w, r, "/settings", "error", "Die CSV-Datei enthält keine Daten (nur Kopfzeile oder leer).")
		return
	}

	accountIDs, err := h.accountIDsByName()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	imported := 0
	var problems []string

	for i, row := range rows[1:] {
		lineNum := i + 2

		rec, account, toAccount, bookedBy, err := parseTransactionRow(row)
		if err != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: %v", lineNum, err))
			continue
		}

		accountID, ok := accountIDs[account]
		if !ok {
			problems = append(problems, fmt.Sprintf("Zeile %d: Konto %q nicht gefunden", lineNum, account))
			continue
		}

		var toAccountIDVal *int64
		if rec.Kind == "transfer" {
			id, ok := accountIDs[toAccount]
			if !ok {
				problems = append(problems, fmt.Sprintf("Zeile %d: Zielkonto %q nicht gefunden", lineNum, toAccount))
				continue
			}
			toAccountIDVal = &id
		}

		userID, err := h.findOrCreateUser(bookedBy)
		if err != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: %v", lineNum, err))
			continue
		}

		if err := h.insertTransactionAndUpdateBalance(accountID, nullInt64(toAccountIDVal), userID, rec); err != nil {
			problems = append(problems, fmt.Sprintf("Zeile %d: %v", lineNum, err))
			continue
		}
		imported++
	}

	finishImport(w, r, "/settings", imported, "Buchung(en)", problems)
}

// parseTransactionRow liest eine CSV-Zeile im Export-Format
// (booked_at,kind,account,to_account,amount,category,description,booked_by).
func parseTransactionRow(row []string) (rec transactionRecord, account, toAccount, bookedBy string, err error) {
	if len(row) < 8 {
		err = fmt.Errorf("erwartet 8 Spalten, gefunden %d", len(row))
		return
	}

	kind := strings.TrimSpace(row[1])
	if kind != "income" && kind != "expense" && kind != "transfer" {
		err = fmt.Errorf("ungültige Buchungsart %q", kind)
		return
	}

	amount, amtErr := parseAmount(row[4])
	if amtErr != nil || amount <= 0 {
		err = fmt.Errorf("ungültiger Betrag %q", row[4])
		return
	}

	bookedAt := strings.TrimSpace(row[0])
	if bookedAt == "" {
		bookedAt = time.Now().Format("2006-01-02")
	}

	rec = transactionRecord{
		BookedAt:    bookedAt,
		Kind:        kind,
		Amount:      amount,
		Category:    strings.TrimSpace(row[5]),
		Description: strings.TrimSpace(row[6]),
	}
	account = strings.TrimSpace(row[2])
	toAccount = strings.TrimSpace(row[3])
	bookedBy = strings.TrimSpace(row[7])
	return
}

func (h *Handlers) accountIDsByName() (map[string]int64, error) {
	rows, err := h.db.Query(`SELECT id, name FROM accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		m[name] = id
	}
	return m, rows.Err()
}

// nullInt64 wandelt einen optionalen Zeiger in sql.NullInt64 um.
func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// finishImport baut aus Erfolgs-/Fehleranzahl eine verständliche
// Hinweis-Meldung und leitet zur angegebenen Seite zurück.
func finishImport(w http.ResponseWriter, r *http.Request, path string, imported int, unit string, problems []string) {
	msg := fmt.Sprintf("%d %s importiert.", imported, unit)
	if len(problems) == 0 {
		redirectWithMessage(w, r, path, "success", msg)
		return
	}
	if len(problems) > 5 {
		problems = append(problems[:5], "…weitere Fehler gekürzt")
	}
	redirectWithMessage(w, r, path, "error", msg+" Übersprungen: "+strings.Join(problems, " | "))
}
