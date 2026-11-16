package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"household-app/internal/models"
)

// Daueraufträge buchen Miete, Lohn, Abos usw. automatisch. Der Scheduler
// (siehe StartScheduler) prüft stündlich und holt verpasste Termine nach,
// z. B. wenn der Pi aus war.

var recurringRunMu sync.Mutex

// recurringIntervals sind die wählbaren Rhythmen (Monate → Bezeichnung).
var recurringIntervals = []struct {
	Months int
	Label  string
}{
	{1, "monatlich"},
	{3, "vierteljährlich"},
	{6, "halbjährlich"},
	{12, "jährlich"},
}

func IntervalLabel(months int) string {
	for _, iv := range recurringIntervals {
		if iv.Months == months {
			return iv.Label
		}
	}
	return fmt.Sprintf("alle %d Monate", months)
}

// addMonthsClamped addiert Monate; gibt es den Tag im Zielmonat nicht,
// wird der Monatsletzte genommen (31.1. + 1 Monat = 28./29.2.).
func addMonthsClamped(start time.Time, months int) time.Time {
	first := time.Date(start.Year(), start.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := first.AddDate(0, 1, -1).Day()
	day := start.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

// nextRecurringDate liefert den nächsten Termin nach der letzten Ausführung
// und nicht vor BookFrom, sofern er nicht nach until (und nicht nach dem
// Enddatum) liegt; sonst "".
func nextRecurringDate(r models.RecurringTransaction, until string) string {
	start, err := time.Parse("2006-01-02", r.StartDate)
	if err != nil || r.EveryMonths <= 0 {
		return ""
	}
	for k := 0; k < 2400; k++ {
		d := addMonthsClamped(start, k*r.EveryMonths).Format("2006-01-02")
		if (r.LastExecutedOn != "" && d <= r.LastExecutedOn) || d < r.BookFrom {
			continue
		}
		if (r.EndDate != "" && d > r.EndDate) || d > until {
			return ""
		}
		return d
	}
	return ""
}

func (h *Handlers) loadRecurring() ([]models.RecurringTransaction, error) {
	rows, err := h.db.Query(`SELECT r.id, r.account_id, a.name, COALESCE(r.to_account_id, 0), COALESCE(b.name, ''),
			r.user_id, r.kind, r.amount, r.category, r.description, r.every_months, r.start_date,
			COALESCE(r.end_date, ''), COALESCE(r.book_from, ''), r.active, COALESCE(r.last_executed_on, ''), COALESCE(r.last_error, '')
		FROM recurring_transactions r
		JOIN accounts a ON a.id = r.account_id
		LEFT JOIN accounts b ON b.id = r.to_account_id
		ORDER BY r.active DESC, r.kind = 'income' DESC, r.amount DESC, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.RecurringTransaction
	for rows.Next() {
		var x models.RecurringTransaction
		if err := rows.Scan(&x.ID, &x.AccountID, &x.AccountName, &x.ToAccountID, &x.ToAccountName,
			&x.UserID, &x.Kind, &x.Amount, &x.Category, &x.Description, &x.EveryMonths, &x.StartDate,
			&x.EndDate, &x.BookFrom, &x.Active, &x.LastExecutedOn, &x.LastError); err != nil {
			return nil, err
		}
		if x.Active {
			x.NextDue = nextRecurringDate(x, "9999-12-31")
		}
		list = append(list, x)
	}
	return list, rows.Err()
}

func (h *Handlers) executeRecurring(x models.RecurringTransaction, date string) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	to := sql.NullInt64{Int64: x.ToAccountID, Valid: x.ToAccountID != 0}
	rec := transactionRecord{
		BookedAt:    date,
		Kind:        x.Kind,
		Amount:      x.Amount,
		Category:    x.Category,
		Description: x.Description,
		RecurringID: x.ID,
	}
	if err := insertTransactionTx(tx, x.AccountID, to, x.UserID, rec, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE recurring_transactions SET last_executed_on = ?, last_error = NULL WHERE id = ?`, date, x.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RunDueRecurring bucht alle fälligen Termine bis einschließlich today.
func (h *Handlers) RunDueRecurring(today string) (executed int, errs []string) {
	recurringRunMu.Lock()
	defer recurringRunMu.Unlock()

	list, err := h.loadRecurring()
	if err != nil {
		return 0, []string{err.Error()}
	}
	for _, x := range list {
		if !x.Active {
			continue
		}
		for i := 0; i < 240; i++ { // Sicherheitsgrenze
			due := nextRecurringDate(x, today)
			if due == "" {
				break
			}
			if err := h.executeRecurring(x, due); err != nil {
				msg := fmt.Sprintf("Dauerauftrag %s (%s): %v", recurringTitle(x), due, err)
				errs = append(errs, msg)
				_, _ = h.db.Exec(`UPDATE recurring_transactions SET last_error = ? WHERE id = ?`, msg, x.ID)
				break
			}
			x.LastExecutedOn = due
			executed++
		}
	}
	return executed, errs
}

func recurringTitle(x models.RecurringTransaction) string {
	switch {
	case x.Description != "":
		return x.Description
	case x.Category != "":
		return x.Category
	case x.Kind == "transfer":
		return "Umbuchung"
	}
	return "ohne Beschreibung"
}

// StartScheduler prüft kurz nach dem Start und danach stündlich, ob
// Daueraufträge oder Sparpläne fällig sind, und bucht sie.
func (h *Handlers) StartScheduler() {
	go func() {
		time.Sleep(20 * time.Second)
		for {
			today := time.Now().Format("2006-01-02")
			if n, errs := h.RunDueRecurring(today); n > 0 || len(errs) > 0 {
				logRun("Daueraufträge", n, errs)
			}
			if n, errs := h.RunDueSavingsPlans(today); n > 0 || len(errs) > 0 {
				logRun("Sparpläne", n, errs)
			}
			time.Sleep(time.Hour)
		}
	}()
}

// recurringRow ist ein Dauerauftrag samt Bearbeiten-Formular für die Seite.
type recurringRow struct {
	models.RecurringTransaction
	Title    string
	Interval string
	Form     txFormView
}

// RecurringPage zeigt alle Daueraufträge und das Formular für neue.
func (h *Handlers) RecurringPage(w http.ResponseWriter, r *http.Request) {
	list, err := h.loadRecurring()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categories, err := h.loadCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var rows []recurringRow
	var monthlyIn, monthlyOut float64
	for _, x := range list {
		rows = append(rows, recurringRow{
			RecurringTransaction: x,
			Title:                recurringTitle(x),
			Interval:             IntervalLabel(x.EveryMonths),
			Form:                 newTxFormView(accounts, categories, x.Kind, x.AccountID, x.ToAccountID, x.Amount, x.Category, x.Description),
		})
		if !x.Active {
			continue
		}
		perMonth := x.Amount / float64(x.EveryMonths)
		switch x.Kind {
		case "income":
			monthlyIn += perMonth
		case "expense":
			monthlyOut += perMonth
		}
	}
	defaultAccount := firstAccountID(accounts)

	data := struct {
		Rows            []recurringRow
		NewForm         txFormView
		Intervals       any
		HasAccounts     bool
		MonthlyIn       float64
		MonthlyOut      float64
		Today           string
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		CurrentUserName string
	}{
		Rows:            rows,
		NewForm:         newTxFormView(accounts, categories, "expense", defaultAccount, 0, 0, "", ""),
		Intervals:       recurringIntervals,
		HasAccounts:     len(accounts) > 0,
		MonthlyIn:       monthlyIn,
		MonthlyOut:      monthlyOut,
		Today:           time.Now().Format("2006-01-02"),
		ErrorMsg:        r.URL.Query().Get("error"),
		SuccessMsg:      r.URL.Query().Get("success"),
		Page:            "home",
		CurrentUserName: currentUser(r).Name,
	}
	if err := h.tmpl.ExecuteTemplate(w, "recurring.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// parseRecurringForm liest Buchungsfelder plus Rhythmus, Start und Ende.
func (h *Handlers) parseRecurringForm(r *http.Request) (f txForm, every int, start, end, msg string, err error) {
	if err = r.ParseForm(); err != nil {
		return
	}
	if f, msg = parseTxForm(r); msg != "" {
		return
	}
	if msg, err = h.checkTxAccounts(f); msg != "" || err != nil {
		return
	}
	every, _ = strconv.Atoi(r.FormValue("every_months"))
	if every != 1 && every != 3 && every != 6 && every != 12 {
		msg = "Ungültiger Rhythmus."
		return
	}
	start = strings.TrimSpace(r.FormValue("start_date"))
	if _, e := time.Parse("2006-01-02", start); e != nil {
		msg = "Bitte das Datum der ersten Buchung angeben."
		return
	}
	end = strings.TrimSpace(r.FormValue("end_date"))
	if end != "" {
		if _, e := time.Parse("2006-01-02", end); e != nil || end < start {
			msg = "Das Enddatum muss nach der ersten Buchung liegen."
			return
		}
	}
	return
}

// CreateRecurring legt einen Dauerauftrag an. Gebucht wird erst ab heute:
// Termine vor dem Anlegen stecken schon im Kontostand und werden nicht
// nachgebucht.
func (h *Handlers) CreateRecurring(w http.ResponseWriter, r *http.Request) {
	f, every, start, end, msg, err := h.parseRecurringForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg != "" {
		redirectWithMessage(w, r, "/recurring", "error", msg)
		return
	}
	rec := f.Record
	if _, err := h.db.Exec(`INSERT INTO recurring_transactions
			(account_id, to_account_id, user_id, kind, amount, category, description, every_months, start_date, end_date, book_from)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.AccountID, f.ToAccountID, currentUser(r).ID, rec.Kind, rec.Amount, rec.Category, rec.Description,
		every, start, sql.NullString{String: end, Valid: end != ""}, today()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.finishRecurringChange(w, r, "Dauerauftrag angelegt.")
}

// UpdateRecurring ändert einen Dauerauftrag. Bereits gebuchte Buchungen
// bleiben unverändert; die Änderung gilt ab dem nächsten Termin (ab heute,
// ein in die Vergangenheit verschobenes Datum wird nicht nachgebucht).
func (h *Handlers) UpdateRecurring(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	f, every, start, end, msg, err := h.parseRecurringForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg != "" {
		redirectWithMessage(w, r, "/recurring", "error", msg)
		return
	}
	rec := f.Record
	res, err := h.db.Exec(`UPDATE recurring_transactions SET account_id = ?, to_account_id = ?, kind = ?, amount = ?,
			category = ?, description = ?, every_months = ?, start_date = ?, end_date = ?, book_from = ?, last_error = NULL
		WHERE id = ?`,
		f.AccountID, f.ToAccountID, rec.Kind, rec.Amount, rec.Category, rec.Description,
		every, start, sql.NullString{String: end, Valid: end != ""}, today(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		redirectWithMessage(w, r, "/recurring", "error", "Dauerauftrag nicht gefunden.")
		return
	}
	h.finishRecurringChange(w, r, "Dauerauftrag gespeichert.")
}

func (h *Handlers) finishRecurringChange(w http.ResponseWriter, r *http.Request, msg string) {
	n, errs := h.RunDueRecurring(today())
	if n > 0 {
		msg += fmt.Sprintf(" %d heute fällige Buchung(en) gebucht.", n)
	}
	if len(errs) > 0 {
		redirectWithMessage(w, r, "/recurring", "error", msg+" Achtung: "+strings.Join(errs, "; "))
		return
	}
	redirectWithMessage(w, r, "/recurring", "success", msg)
}

// ToggleRecurring pausiert einen Dauerauftrag bzw. setzt ihn fort. Beim
// Fortsetzen werden die pausierten Termine nicht nachgeholt.
func (h *Handlers) ToggleRecurring(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	var active bool
	if err := h.db.QueryRow(`SELECT active FROM recurring_transactions WHERE id = ?`, id).Scan(&active); err != nil {
		redirectWithMessage(w, r, "/recurring", "error", "Dauerauftrag nicht gefunden.")
		return
	}
	if active {
		_, err = h.db.Exec(`UPDATE recurring_transactions SET active = 0 WHERE id = ?`, id)
	} else {
		_, err = h.db.Exec(`UPDATE recurring_transactions SET active = 1, last_error = NULL, book_from = ? WHERE id = ?`, today(), id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if active {
		redirectWithMessage(w, r, "/recurring", "success", "Dauerauftrag pausiert.")
	} else {
		redirectWithMessage(w, r, "/recurring", "success", "Dauerauftrag fortgesetzt (ohne die Pause nachzuholen).")
	}
}

// DeleteRecurring löscht einen Dauerauftrag. Bereits gebuchte Buchungen
// bleiben erhalten.
func (h *Handlers) DeleteRecurring(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE transactions SET recurring_id = NULL WHERE recurring_id = ?`,
		`DELETE FROM recurring_transactions WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectWithMessage(w, r, "/recurring", "success", "Dauerauftrag gelöscht. Bereits gebuchte Buchungen bleiben erhalten.")
}

func logRun(what string, n int, errs []string) {
	if n > 0 {
		log.Printf("%s: %d Ausführung(en) gebucht", what, n)
	}
	for _, e := range errs {
		log.Printf("%s: %s", what, e)
	}
}

// ScheduleFields füllt das Template "recurring-schedule" (Rhythmus,
// erste Buchung, Enddatum); als Template-Funktion "schedule" registriert.
func ScheduleFields(intervals any, every int, start, end string) map[string]any {
	return map[string]any{"Intervals": intervals, "Every": every, "Start": start, "End": end}
}

func today() string { return time.Now().Format("2006-01-02") }

// Dict baut im Template eine Map aus Schlüssel/Wert-Paaren, um Teil-Templates
// mehrere Werte zu übergeben: {{template "x" (dict "A" 1 "B" 2)}}.
func Dict(pairs ...any) map[string]any {
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		if k, ok := pairs[i].(string); ok {
			m[k] = pairs[i+1]
		}
	}
	return m
}
