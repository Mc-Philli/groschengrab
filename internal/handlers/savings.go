package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"household-app/internal/models"
)

// Sparpläne kaufen an einem festen Tag im Monat für einen festen CHF-Betrag
// ETF-Anteile, aufgeteilt nach Prozent. Die App rechnet mit dem Schlusskurs
// des Ausführungstags (bzw. des letzten Handelstags davor) und dem
// Wechselkurs desselben Tages. Gebühren und der exakte Ausführungskurs des
// Brokers sind nicht bekannt – kleine Abweichungen gleicht man über
// "Anzahl abgleichen" aus.

func (h *Handlers) loadSavingsPlans() ([]models.SavingsPlan, error) {
	rows, err := h.db.Query(`SELECT p.id, p.depot, p.amount, p.day_of_month, p.start_date, COALESCE(p.book_from, ''), p.account_id,
			COALESCE(a.name, ''), p.user_id, p.active, COALESCE(p.last_executed_on, ''), COALESCE(p.last_error, '')
		FROM savings_plans p LEFT JOIN accounts a ON a.id = p.account_id
		ORDER BY p.depot, p.id`)
	if err != nil {
		return nil, err
	}
	var plans []models.SavingsPlan
	for rows.Next() {
		var p models.SavingsPlan
		var acc sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Depot, &p.Amount, &p.DayOfMonth, &p.StartDate, &p.BookFrom, &acc,
			&p.AccountName, &p.UserID, &p.Active, &p.LastExecutedOn, &p.LastError); err != nil {
			rows.Close()
			return nil, err
		}
		if acc.Valid {
			id := acc.Int64
			p.AccountID = &id
		}
		plans = append(plans, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range plans {
		items, err := h.db.Query(`SELECT ticker, name, weight FROM savings_plan_items WHERE plan_id = ? ORDER BY weight DESC, id`, plans[i].ID)
		if err != nil {
			return nil, err
		}
		for items.Next() {
			var it models.SavingsPlanItem
			if err := items.Scan(&it.Ticker, &it.Name, &it.Weight); err != nil {
				items.Close()
				return nil, err
			}
			plans[i].Items = append(plans[i].Items, it)
		}
		items.Close()
		if d := nextExecutionDate(plans[i], "9999-12-31"); d != "" {
			plans[i].NextDue = nextBusinessDay(d)
		}
	}
	return plans, nil
}

// nextExecutionDate liefert den nächsten Ausführungstag nach der letzten
// Ausführung (bzw. ab Startdatum, frühestens ab BookFrom), sofern er nicht
// nach until liegt; sonst "".
func nextExecutionDate(p models.SavingsPlan, until string) string {
	begin, err := time.Parse("2006-01-02", p.StartDate)
	if err != nil {
		return ""
	}
	if p.LastExecutedOn != "" {
		last, err := time.Parse("2006-01-02", p.LastExecutedOn)
		if err == nil && !last.Before(begin) {
			begin = last.AddDate(0, 0, 1)
		}
	}
	if from, err := time.Parse("2006-01-02", p.BookFrom); err == nil && from.After(begin) {
		begin = from
	}
	d := time.Date(begin.Year(), begin.Month(), p.DayOfMonth, 0, 0, 0, 0, time.UTC)
	if d.Before(begin) {
		d = time.Date(begin.Year(), begin.Month()+1, p.DayOfMonth, 0, 0, 0, 0, time.UTC)
	}
	s := d.Format("2006-01-02")
	if s > until {
		return ""
	}
	return s
}

// executeSavingsPlan führt einen Sparplan für den Termin due aus: je ETF eine
// neue Tranche, optional eine Belastung des Kontos, beides mit Kurs und Datum
// von date (nächster Werktag). Entweder alles oder nichts wird gespeichert.
func (h *Handlers) executeSavingsPlan(p models.SavingsPlan, due, date string) error {
	type lot struct {
		ticker, name string
		qty, price   float64
	}
	var lots []lot
	for _, it := range p.Items {
		price, _, rate, err := priceOn(h.market, it.Ticker, date)
		if err != nil {
			return err
		}
		amountCHF := p.Amount * it.Weight / 100
		qty := amountCHF / (price * rate)
		qty = math.Round(qty*1e6) / 1e6
		lots = append(lots, lot{it.Ticker, it.Name, qty, price})
	}

	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, l := range lots {
		if _, err := tx.Exec(`INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date, depot, name, source, savings_plan_id)
			VALUES (?, ?, ?, ?, ?, ?, 'savings_plan', ?)`,
			l.ticker, l.qty, l.price, date, p.Depot, l.name, p.ID); err != nil {
			return err
		}
	}
	if p.AccountID != nil {
		rec := transactionRecord{
			BookedAt:    date,
			Kind:        "expense",
			Amount:      p.Amount,
			Category:    "Sparplan",
			Description: "Sparplan " + depotDisplayName(p.Depot),
		}
		if err := insertTransactionTx(tx, *p.AccountID, sql.NullInt64{}, p.UserID, rec, ""); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE savings_plans SET last_executed_on = ?, last_error = NULL WHERE id = ?`, due, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// RunDueSavingsPlans führt alle fälligen Ausführungen aus, auch verpasste
// (z. B. wenn der Pi aus war). Schlägt eine Ausführung fehl (meist: Kurs
// nicht verfügbar), wird sie beim nächsten Lauf erneut versucht.
func (h *Handlers) RunDueSavingsPlans(today string) (executed int, errs []string) {
	// Stündliche Prüfung und Knopf "Jetzt ausführen" dürfen nicht gleichzeitig
	// laufen, sonst würde doppelt gebucht.
	savingsRunMu.Lock()
	defer savingsRunMu.Unlock()

	plans, err := h.loadSavingsPlans()
	if err != nil {
		return 0, []string{err.Error()}
	}
	for _, p := range plans {
		if !p.Active || len(p.Items) == 0 {
			continue
		}
		for i := 0; i < 120; i++ { // Sicherheitsgrenze: max. 10 Jahre nachholen
			due := nextExecutionDate(p, today)
			if due == "" {
				break
			}
			// Wochenende/Feiertag: erst am nächsten Werktag kaufen.
			bookOn := nextBusinessDay(due)
			if bookOn > today {
				break
			}
			if err := h.executeSavingsPlan(p, due, bookOn); err != nil {
				msg := fmt.Sprintf("Sparplan %s (%s): %v", depotDisplayName(p.Depot), due, err)
				errs = append(errs, msg)
				_, _ = h.db.Exec(`UPDATE savings_plans SET last_error = ? WHERE id = ?`, msg, p.ID)
				break
			}
			p.LastExecutedOn = due
			executed++
		}
	}
	return executed, errs
}

var savingsRunMu sync.Mutex

// savingsPlanForm sind die Felder des Sparplan-Formulars (Anlegen und
// Bearbeiten).
type savingsPlanForm struct {
	depot     string
	amount    float64
	day       int
	start     string
	accountID sql.NullInt64
	items     []models.SavingsPlanItem
}

// parseSavingsPlanForm prüft das Formular; msg ist bei Fehleingaben gesetzt.
func parseSavingsPlanForm(r *http.Request) (f savingsPlanForm, msg string) {
	if f.depot = depotFormValue(r, "depot"); f.depot == "" {
		return f, "Bitte ein Depot auswählen oder ein neues anlegen."
	}
	var err error
	if f.amount, err = parseAmount(r.FormValue("amount")); err != nil || f.amount <= 0 {
		return f, "Bitte einen gültigen Betrag größer 0 angeben."
	}
	if f.day, err = strconv.Atoi(r.FormValue("day_of_month")); err != nil || f.day < 1 || f.day > 28 {
		return f, "Ausführungstag muss zwischen 1 und 28 liegen."
	}
	f.start = strings.TrimSpace(r.FormValue("start_date"))
	if _, err := time.Parse("2006-01-02", f.start); err != nil {
		return f, "Bitte ein gültiges Startdatum angeben."
	}
	if v := r.FormValue("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return f, "Ungültiges Konto."
		}
		f.accountID = sql.NullInt64{Int64: id, Valid: true}
	}

	tickers := r.Form["item_ticker"]
	weights := r.Form["item_weight"]
	var sum float64
	for i := range tickers {
		t := strings.ToUpper(strings.TrimSpace(tickers[i]))
		if t == "" {
			continue
		}
		if i >= len(weights) || strings.TrimSpace(weights[i]) == "" {
			return f, fmt.Sprintf("Bitte für %s einen Anteil in Prozent angeben.", t)
		}
		wgt, err := parseAmount(weights[i])
		if err != nil || wgt <= 0 {
			return f, fmt.Sprintf("Ungültiger Anteil für %s.", t)
		}
		f.items = append(f.items, models.SavingsPlanItem{Ticker: t, Weight: wgt})
		sum += wgt
	}
	if len(f.items) == 0 {
		return f, "Bitte mindestens einen ETF angeben."
	}
	if math.Abs(sum-100) > 0.01 {
		return f, fmt.Sprintf("Die Anteile ergeben zusammen %.2f %% statt 100 %%.", sum)
	}
	return f, ""
}

// saveSavingsPlanItems ersetzt die ETF-Aufteilung eines Sparplans. Der
// Anzeigename wird von einer vorhandenen Position übernommen.
func saveSavingsPlanItems(tx *sql.Tx, planID int64, items []models.SavingsPlanItem) error {
	if _, err := tx.Exec(`DELETE FROM savings_plan_items WHERE plan_id = ?`, planID); err != nil {
		return err
	}
	for _, it := range items {
		var name string
		_ = tx.QueryRow(`SELECT name FROM holdings WHERE ticker = ? AND name != '' LIMIT 1`, it.Ticker).Scan(&name)
		if _, err := tx.Exec(`INSERT INTO savings_plan_items (plan_id, ticker, name, weight) VALUES (?, ?, ?, ?)`,
			planID, it.Ticker, name, it.Weight); err != nil {
			return err
		}
	}
	return nil
}

// CreateSavingsPlan verarbeitet das Formular "Sparplan anlegen". Gekauft
// wird erst ab heute: frühere Termine sind in der Regel schon als Käufe
// erfasst und werden nicht nachgebucht.
func (h *Handlers) CreateSavingsPlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, msg := parseSavingsPlanForm(r)
	if msg != "" {
		redirectWithMessage(w, r, "/depot", "error", msg)
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO savings_plans (depot, amount, day_of_month, start_date, book_from, account_id, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.depot, f.amount, f.day, f.start, today(), f.accountID, currentUser(r).ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	planID, _ := res.LastInsertId()
	if err := saveSavingsPlanItems(tx, planID, f.items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.finishSavingsPlanChange(w, r, "Sparplan angelegt.")
}

// UpdateSavingsPlan ändert Depot, Betrag, Termin, Konto und ETF-Aufteilung.
// Bereits gebuchte Käufe bleiben unverändert; die Änderung gilt ab heute.
func (h *Handlers) UpdateSavingsPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, msg := parseSavingsPlanForm(r)
	if msg != "" {
		redirectWithMessage(w, r, "/depot", "error", msg)
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE savings_plans SET depot = ?, amount = ?, day_of_month = ?, start_date = ?,
			book_from = ?, account_id = ?, last_error = NULL WHERE id = ?`,
		f.depot, f.amount, f.day, f.start, today(), f.accountID, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		redirectWithMessage(w, r, "/depot", "error", "Sparplan nicht gefunden.")
		return
	}
	if err := saveSavingsPlanItems(tx, id, f.items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.finishSavingsPlanChange(w, r, "Sparplan gespeichert.")
}

// finishSavingsPlanChange führt einen heute fälligen Termin gleich aus.
func (h *Handlers) finishSavingsPlanChange(w http.ResponseWriter, r *http.Request, msg string) {
	n, errs := h.RunDueSavingsPlans(today())
	if n > 0 {
		msg += fmt.Sprintf(" %d heute fällige Ausführung(en) gebucht.", n)
	}
	if len(errs) > 0 {
		redirectWithMessage(w, r, "/depot", "error", msg+" Achtung: "+strings.Join(errs, "; "))
		return
	}
	redirectWithMessage(w, r, "/depot", "success", msg)
}

// ToggleSavingsPlan pausiert einen Sparplan bzw. setzt ihn fort. Beim
// Fortsetzen werden die pausierten Monate nicht nachgeholt.
func (h *Handlers) ToggleSavingsPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	var active bool
	if err := h.db.QueryRow(`SELECT active FROM savings_plans WHERE id = ?`, id).Scan(&active); err != nil {
		redirectWithMessage(w, r, "/depot", "error", "Sparplan nicht gefunden.")
		return
	}
	if active {
		_, err = h.db.Exec(`UPDATE savings_plans SET active = 0 WHERE id = ?`, id)
	} else {
		// Ab heute weiter, ohne die Pause nachzuholen.
		_, err = h.db.Exec(`UPDATE savings_plans SET active = 1, last_error = NULL, book_from = ? WHERE id = ?`, today(), id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if active {
		redirectWithMessage(w, r, "/depot", "success", "Sparplan pausiert.")
	} else {
		redirectWithMessage(w, r, "/depot", "success", "Sparplan fortgesetzt.")
	}
}

// DeleteSavingsPlan löscht einen Sparplan. Bereits gebuchte Käufe bleiben
// im Depot erhalten.
func (h *Handlers) DeleteSavingsPlan(w http.ResponseWriter, r *http.Request) {
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
		`UPDATE holdings SET savings_plan_id = NULL WHERE savings_plan_id = ?`,
		`DELETE FROM savings_plan_items WHERE plan_id = ?`,
		`DELETE FROM savings_plans WHERE id = ?`,
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
	redirectWithMessage(w, r, "/depot", "success", "Sparplan gelöscht. Bereits gebuchte Käufe bleiben im Depot.")
}

// RunSavingsPlansNow führt fällige Sparpläne sofort aus (statt auf die
// stündliche Prüfung zu warten).
func (h *Handlers) RunSavingsPlansNow(w http.ResponseWriter, r *http.Request) {
	n, errs := h.RunDueSavingsPlans(time.Now().Format("2006-01-02"))
	if len(errs) > 0 {
		redirectWithMessage(w, r, "/depot", "error", strings.Join(errs, "; "))
		return
	}
	if n == 0 {
		redirectWithMessage(w, r, "/depot", "success", "Keine fälligen Sparplan-Ausführungen.")
		return
	}
	redirectWithMessage(w, r, "/depot", "success", fmt.Sprintf("%d Sparplan-Ausführung(en) gebucht.", n))
}
