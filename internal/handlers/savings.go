package handlers

import (
	"database/sql"
	"fmt"
	"log"
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
	rows, err := h.db.Query(`SELECT p.id, p.depot, p.amount, p.day_of_month, p.start_date, p.account_id,
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
		if err := rows.Scan(&p.ID, &p.Depot, &p.Amount, &p.DayOfMonth, &p.StartDate, &acc,
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
		plans[i].NextDue = nextExecutionDate(plans[i], "9999-12-31")
	}
	return plans, nil
}

// nextExecutionDate liefert den nächsten Ausführungstag nach der letzten
// Ausführung (bzw. ab Startdatum), sofern er nicht nach until liegt; sonst "".
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

// executeSavingsPlan führt einen Sparplan für einen Stichtag aus: je ETF eine
// neue Tranche, optional eine Belastung des Kontos. Entweder alles oder
// nichts wird gespeichert.
func (h *Handlers) executeSavingsPlan(p models.SavingsPlan, date string) error {
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
	if _, err := tx.Exec(`UPDATE savings_plans SET last_executed_on = ?, last_error = NULL WHERE id = ?`, date, p.ID); err != nil {
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
			if err := h.executeSavingsPlan(p, due); err != nil {
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

// StartSavingsPlanScheduler prüft kurz nach dem Start und danach stündlich,
// ob Sparpläne fällig sind.
func (h *Handlers) StartSavingsPlanScheduler() {
	go func() {
		time.Sleep(20 * time.Second)
		for {
			n, errs := h.RunDueSavingsPlans(time.Now().Format("2006-01-02"))
			if n > 0 {
				log.Printf("Sparpläne: %d Ausführung(en) gebucht", n)
			}
			for _, e := range errs {
				log.Printf("Sparpläne: %s", e)
			}
			time.Sleep(time.Hour)
		}
	}()
}

// CreateSavingsPlan verarbeitet das Formular "Sparplan anlegen".
func (h *Handlers) CreateSavingsPlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fail := func(msg string) { redirectWithMessage(w, r, "/depot", "error", msg) }

	depot := strings.TrimSpace(r.FormValue("depot"))
	if depot == "" {
		fail("Bitte ein Depot angeben (z. B. Saxo oder WillBe).")
		return
	}
	amount, err := parseAmount(r.FormValue("amount"))
	if err != nil || amount <= 0 {
		fail("Bitte einen gültigen Betrag größer 0 angeben.")
		return
	}
	day, err := strconv.Atoi(r.FormValue("day_of_month"))
	if err != nil || day < 1 || day > 28 {
		fail("Ausführungstag muss zwischen 1 und 28 liegen.")
		return
	}
	start := strings.TrimSpace(r.FormValue("start_date"))
	if _, err := time.Parse("2006-01-02", start); err != nil {
		fail("Bitte ein gültiges Startdatum angeben.")
		return
	}
	var accountID sql.NullInt64
	if v := r.FormValue("account_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fail("Ungültiges Konto.")
			return
		}
		accountID = sql.NullInt64{Int64: id, Valid: true}
	}

	tickers := r.Form["item_ticker"]
	weights := r.Form["item_weight"]
	var items []models.SavingsPlanItem
	var sum float64
	for i := range tickers {
		t := strings.ToUpper(strings.TrimSpace(tickers[i]))
		if t == "" {
			continue
		}
		if i >= len(weights) {
			fail("Bitte zu jedem ETF einen Anteil in Prozent angeben.")
			return
		}
		wgt, err := parseAmount(weights[i])
		if err != nil || wgt <= 0 {
			fail(fmt.Sprintf("Ungültiger Anteil für %s.", t))
			return
		}
		items = append(items, models.SavingsPlanItem{Ticker: t, Weight: wgt})
		sum += wgt
	}
	if len(items) == 0 {
		fail("Bitte mindestens einen ETF angeben.")
		return
	}
	if math.Abs(sum-100) > 0.01 {
		fail(fmt.Sprintf("Die Anteile ergeben zusammen %.2f %% statt 100 %%.", sum))
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO savings_plans (depot, amount, day_of_month, start_date, account_id, user_id) VALUES (?, ?, ?, ?, ?, ?)`,
		depot, amount, day, start, accountID, currentUser(r).ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	planID, _ := res.LastInsertId()
	for _, it := range items {
		var name string
		_ = tx.QueryRow(`SELECT name FROM holdings WHERE ticker = ? AND name != '' LIMIT 1`, it.Ticker).Scan(&name)
		if _, err := tx.Exec(`INSERT INTO savings_plan_items (plan_id, ticker, name, weight) VALUES (?, ?, ?, ?)`,
			planID, it.Ticker, name, it.Weight); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Liegen Ausführungstage schon in der Vergangenheit, gleich nachholen.
	n, errs := h.RunDueSavingsPlans(time.Now().Format("2006-01-02"))
	msg := "Sparplan angelegt."
	if n > 0 {
		msg += fmt.Sprintf(" %d vergangene Ausführung(en) nachgebucht.", n)
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
		_, err = h.db.Exec(`UPDATE savings_plans SET active = 1, last_error = NULL,
			last_executed_on = MAX(COALESCE(last_executed_on, ''), date('now', '-1 day')) WHERE id = ?`, id)
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
