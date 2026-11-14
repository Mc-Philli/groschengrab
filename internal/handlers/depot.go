package handlers

import (
	"database/sql"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"household-app/internal/models"
	"household-app/internal/stocks"
)

// Depot-Namen, die im Formular immer vorgeschlagen werden.
var defaultDepotNames = []string{"Saxo", "WillBe"}

const noDepotName = "Ohne Depot"

// Depot zeigt alle Positionen gruppiert nach Depot, mit live abgefragten
// Kursen und Umrechnung in CHF, sowie die Sparpläne.
func (h *Handlers) Depot(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.loadHoldings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	depots, totalCHF, gainCHF, missing := h.buildDepotViews(holdings)

	plans, err := h.loadSavingsPlans()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Depots          []models.DepotView
		DepotNames      []string
		Tickers         []string
		TotalValueCHF   float64
		TotalGainCHF    float64
		Missing         int
		SavingsPlans    []models.SavingsPlan
		Accounts        []models.Account
		PlanItemRows    []int
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		Today           string
		CurrentUserName string
	}{
		Depots:          depots,
		DepotNames:      depotNameSuggestions(holdings, plans),
		Tickers:         tickerSuggestions(holdings),
		TotalValueCHF:   totalCHF,
		TotalGainCHF:    gainCHF,
		Missing:         missing,
		SavingsPlans:    plans,
		Accounts:        accounts,
		PlanItemRows:    []int{1, 2, 3, 4, 5},
		ErrorMsg:        r.URL.Query().Get("error"),
		SuccessMsg:      r.URL.Query().Get("success"),
		Page:            "depot",
		Today:           time.Now().Format("2006-01-02"),
		CurrentUserName: currentUser(r).Name,
	}

	if err := h.tmpl.ExecuteTemplate(w, "depot.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func depotNameSuggestions(holdings []models.Holding, plans []models.SavingsPlan) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, n := range defaultDepotNames {
		add(n)
	}
	for _, hd := range holdings {
		add(hd.Depot)
	}
	for _, p := range plans {
		add(p.Depot)
	}
	return names
}

func tickerSuggestions(holdings []models.Holding) []string {
	seen := map[string]bool{}
	var list []string
	for _, hd := range holdings {
		if !seen[hd.Ticker] {
			seen[hd.Ticker] = true
			list = append(list, hd.Ticker)
		}
	}
	sort.Strings(list)
	return list
}

func depotDisplayName(depot string) string {
	if depot == "" {
		return noDepotName
	}
	return depot
}

// buildDepotViews fasst die Tranchen zu Positionen zusammen, fragt Kurse und
// Wechselkurse parallel ab und rechnet alles in CHF um. missing zählt
// Positionen, deren Wert mangels Kurs nicht in den Summen enthalten ist.
func (h *Handlers) buildDepotViews(holdings []models.Holding) (depots []models.DepotView, totalCHF, gainCHF float64, missing int) {
	type key struct{ depot, ticker string }
	positions := map[key]*models.PositionView{}
	var order []key
	for _, hd := range holdings {
		k := key{hd.Depot, hd.Ticker}
		p, ok := positions[k]
		if !ok {
			p = &models.PositionView{Depot: hd.Depot, Ticker: hd.Ticker}
			positions[k] = p
			order = append(order, k)
		}
		if p.Name == "" && hd.Name != "" {
			p.Name = hd.Name
		}
		p.Quantity += hd.Quantity
		p.CostBasis += hd.Quantity * hd.PurchasePrice
		p.Lots = append(p.Lots, hd)
	}

	// Kurse je Ticker (nicht je Position) abfragen, parallel.
	quotes := map[string]stocks.Quote{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, t := range tickerSuggestions(holdings) {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			if q, err := h.market.Quote(t); err == nil {
				mu.Lock()
				quotes[t] = q
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()

	rates := map[string]float64{}
	for _, q := range quotes {
		if _, done := rates[q.Currency]; done {
			continue
		}
		if r, err := h.market.CHFRate(q.Currency); err == nil {
			rates[q.Currency] = r
		} else {
			rates[q.Currency] = 0
		}
	}

	byDepot := map[string]*models.DepotView{}
	var depotOrder []string
	for _, k := range order {
		p := positions[k]
		roundQuantity(&p.Quantity)
		if p.Quantity != 0 {
			p.AvgPrice = p.CostBasis / p.Quantity
		}
		if q, ok := quotes[p.Ticker]; ok {
			p.PriceAvailable = true
			p.CurrentPrice = q.Price
			p.Currency = q.Currency
			p.CurrentValue = p.Quantity * q.Price
			p.GainAbsolute = p.CurrentValue - p.CostBasis
			if p.CostBasis != 0 {
				p.GainPercent = p.GainAbsolute / p.CostBasis * 100
			}
			if rate := rates[q.Currency]; rate > 0 {
				p.FXAvailable = true
				p.ValueCHF = p.CurrentValue * rate
				// Vereinfachung: Einstandswert zum heutigen Wechselkurs.
				p.GainCHF = p.GainAbsolute * rate
			}
		}

		name := depotDisplayName(p.Depot)
		d, ok := byDepot[name]
		if !ok {
			d = &models.DepotView{Name: name}
			byDepot[name] = d
			depotOrder = append(depotOrder, name)
		}
		d.Positions = append(d.Positions, *p)
		if p.FXAvailable {
			d.ValueCHF += p.ValueCHF
			d.GainCHF += p.GainCHF
		} else if p.Quantity != 0 {
			d.Missing++
		}
	}

	// Depots alphabetisch, "Ohne Depot" ans Ende.
	sort.Slice(depotOrder, func(i, j int) bool {
		if (depotOrder[i] == noDepotName) != (depotOrder[j] == noDepotName) {
			return depotOrder[j] == noDepotName
		}
		return strings.ToLower(depotOrder[i]) < strings.ToLower(depotOrder[j])
	})
	for _, name := range depotOrder {
		d := byDepot[name]
		depots = append(depots, *d)
		totalCHF += d.ValueCHF
		gainCHF += d.GainCHF
		missing += d.Missing
	}
	return depots, totalCHF, gainCHF, missing
}

// roundQuantity entfernt Fließkomma-Reste (z. B. 9.999999999 statt 10).
func roundQuantity(q *float64) {
	*q = math.Round(*q*1e6) / 1e6
}

// depotValueCHF liefert den aktuellen Gesamtwert aller Depots in CHF.
func (h *Handlers) depotValueCHF() (total float64, missing int, err error) {
	holdings, err := h.loadHoldings()
	if err != nil {
		return 0, 0, err
	}
	if len(holdings) == 0 {
		return 0, 0, nil
	}
	_, total, _, missing = h.buildDepotViews(holdings)
	return total, missing, nil
}

func (h *Handlers) loadHoldings() ([]models.Holding, error) {
	rows, err := h.db.Query(`SELECT id, ticker, quantity, purchase_price, purchase_date, depot, name, source, savings_plan_id
		FROM holdings ORDER BY depot, ticker, purchase_date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Holding
	for rows.Next() {
		var hd models.Holding
		var planID sql.NullInt64
		if err := rows.Scan(&hd.ID, &hd.Ticker, &hd.Quantity, &hd.PurchasePrice, &hd.PurchaseDate,
			&hd.Depot, &hd.Name, &hd.Source, &planID); err != nil {
			return nil, err
		}
		if planID.Valid {
			id := planID.Int64
			hd.SavingsPlanID = &id
		}
		list = append(list, hd)
	}
	return list, rows.Err()
}

// CreateHolding verarbeitet das Formular "Kauf erfassen". Der Ticker wird
// nicht zwingend validiert, um bei kurzzeitigen Netzwerkproblemen keine
// gültige Eingabe zu blockieren – schlägt die Kursabfrage fehl, wird der Kauf
// trotzdem gespeichert, aber mit einem Warnhinweis quittiert.
func (h *Handlers) CreateHolding(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ticker := strings.ToUpper(strings.TrimSpace(r.FormValue("ticker")))
	if ticker == "" {
		redirectWithMessage(w, r, "/depot", "error", "Ticker darf nicht leer sein.")
		return
	}
	depot := strings.TrimSpace(r.FormValue("depot"))
	name := strings.TrimSpace(r.FormValue("name"))

	quantity, err := parseAmount(r.FormValue("quantity"))
	if err != nil || quantity <= 0 {
		redirectWithMessage(w, r, "/depot", "error", "Bitte eine gültige Anzahl größer 0 angeben.")
		return
	}

	purchasePrice, err := parseAmount(r.FormValue("purchase_price"))
	if err != nil || purchasePrice <= 0 {
		redirectWithMessage(w, r, "/depot", "error", "Bitte einen gültigen Kaufpreis größer 0 angeben.")
		return
	}

	purchaseDate := strings.TrimSpace(r.FormValue("purchase_date"))
	if purchaseDate == "" {
		purchaseDate = time.Now().Format("2006-01-02")
	}

	// Name von bestehenden Tranchen übernehmen, falls nicht angegeben.
	if name == "" {
		_ = h.db.QueryRow(`SELECT name FROM holdings WHERE ticker = ? AND name != '' LIMIT 1`, ticker).Scan(&name)
	}

	if _, err := h.db.Exec(
		`INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date, depot, name, source) VALUES (?, ?, ?, ?, ?, ?, 'manual')`,
		ticker, quantity, purchasePrice, purchaseDate, depot, name,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := h.market.Quote(ticker); err != nil {
		redirectWithMessage(w, r, "/depot", "error", fmt.Sprintf(
			"Kauf gespeichert, aber Kurs für %q konnte nicht abgerufen werden. Bitte Schreibweise prüfen oder die ISIN-Suche verwenden.", ticker,
		))
		return
	}

	redirectWithMessage(w, r, "/depot", "success", "Kauf erfasst.")
}

// DeleteHolding entfernt eine einzelne Tranche.
func (h *Handlers) DeleteHolding(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`DELETE FROM holdings WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/depot", "success", "Kauf gelöscht.")
}

// ReconcilePosition gleicht eine Position auf die tatsächliche Anzahl beim
// Broker ab. Die Differenz wird als Korrektur-Tranche zum aktuellen Kurs
// gebucht, damit der Gewinn/Verlust dadurch nicht verfälscht wird.
func (h *Handlers) ReconcilePosition(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	depot := r.FormValue("depot")
	ticker := r.FormValue("ticker")
	actual, err := parseAmount(r.FormValue("actual_quantity"))
	if err != nil || actual < 0 || strings.TrimSpace(r.FormValue("actual_quantity")) == "" {
		redirectWithMessage(w, r, "/depot", "error", "Bitte die tatsächliche Anzahl angeben (0 oder größer).")
		return
	}

	var current float64
	var name string
	if err := h.db.QueryRow(`SELECT COALESCE(SUM(quantity), 0), COALESCE(MAX(name), '') FROM holdings WHERE depot = ? AND ticker = ?`,
		depot, ticker).Scan(&current, &name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	diff := actual - current
	roundQuantity(&diff)
	if diff == 0 {
		redirectWithMessage(w, r, "/depot", "success", "Anzahl stimmt bereits überein.")
		return
	}

	q, err := h.market.Quote(ticker)
	if err != nil {
		redirectWithMessage(w, r, "/depot", "error", "Abgleich nicht möglich: aktueller Kurs nicht verfügbar. Bitte später erneut versuchen.")
		return
	}

	if _, err := h.db.Exec(
		`INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date, depot, name, source) VALUES (?, ?, ?, ?, ?, ?, 'correction')`,
		ticker, diff, q.Price, time.Now().Format("2006-01-02"), depot, name,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/depot", "success", fmt.Sprintf("%s abgeglichen: Korrektur um %s Stück.", ticker, formatQuantity(diff)))
}

// MovePosition ordnet alle Tranchen einer Position einem anderen Depot zu
// und setzt optional den Anzeigenamen.
func (h *Handlers) MovePosition(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from := r.FormValue("depot")
	ticker := r.FormValue("ticker")
	to := strings.TrimSpace(r.FormValue("new_depot"))
	name := strings.TrimSpace(r.FormValue("name"))

	if _, err := h.db.Exec(`UPDATE holdings SET depot = ?, name = ? WHERE depot = ? AND ticker = ?`,
		to, name, from, ticker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectWithMessage(w, r, "/depot", "success", fmt.Sprintf("%s gespeichert (Depot: %s).", ticker, depotDisplayName(to)))
}

func formatQuantity(q float64) string {
	s := strconv.FormatFloat(q, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if q > 0 {
		s = "+" + s
	}
	return s
}

// FormatQuantity zeigt Stückzahlen ohne überflüssige Nachkommastellen an
// (Template-Funktion "qty").
func FormatQuantity(q float64) string {
	return strings.TrimPrefix(formatQuantity(q), "+")
}

// TickerSearch beantwortet die ISIN-/Namenssuche im Kaufformular (htmx) mit
// einer Liste anklickbarer Treffer.
func (h *Handlers) TickerSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(q) < 2 {
		fmt.Fprint(w, `<p class="hint-text">Bitte ISIN, Namen oder Kürzel eingeben.</p>`)
		return
	}
	results, err := stocks.Search(q)
	if err != nil {
		fmt.Fprintf(w, `<p class="hint-text">Suche fehlgeschlagen: %s</p>`, template.HTMLEscapeString(err.Error()))
		return
	}
	if len(results) == 0 {
		fmt.Fprint(w, `<p class="hint-text">Nichts gefunden. Tipp: Bei einer ISIN ohne Treffer nach dem Fondsnamen suchen.</p>`)
		return
	}
	fmt.Fprint(w, `<ul class="search-results">`)
	for _, res := range results {
		fmt.Fprintf(w, `<li><button type="button" class="btn btn-small search-pick" data-symbol="%s" data-name="%s">%s</button> %s <span class="hint-text">%s · %s</span></li>`,
			template.HTMLEscapeString(res.Symbol), template.HTMLEscapeString(res.Name),
			template.HTMLEscapeString(res.Symbol), template.HTMLEscapeString(res.Name),
			template.HTMLEscapeString(res.Exchange), template.HTMLEscapeString(res.Type))
	}
	fmt.Fprint(w, `</ul>`)
}
