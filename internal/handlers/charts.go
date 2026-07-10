package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"household-app/internal/stocks"
)

// ChartPoint ist ein einzelner Datenpunkt für ein Zeitreihen-Diagramm.
type ChartPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

func respondJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(data)
}

// startDateForRange bildet eine UI-Zeitraum-Auswahl auf ein Startdatum ab.
func startDateForRange(uiRange string, earliestTransactionDate string) time.Time {
	now := time.Now()
	switch uiRange {
	case "1d":
		return now.AddDate(0, 0, -1)
	case "1m":
		return now.AddDate(0, -1, 0)
	case "3m":
		return now.AddDate(0, -3, 0)
	case "6m":
		return now.AddDate(0, -6, 0)
	case "1y":
		return now.AddDate(-1, 0, 0)
	case "max":
		if earliestTransactionDate != "" {
			if t, err := time.Parse("2006-01-02", earliestTransactionDate); err == nil {
				return t
			}
		}
		return now
	default:
		return now.AddDate(0, -3, 0)
	}
}

// NetWorthHistory liefert die Entwicklung des Gesamtvermögens (Summe aller
// Kontosalden) für den angefragten Zeitraum. Wird komplett aus dem
// bestehenden Buchungsverlauf rekonstruiert – Transfers zählen dabei nicht
// mit, da sie netto keine Auswirkung auf das Gesamtvermögen haben.
func (h *Handlers) NetWorthHistory(w http.ResponseWriter, r *http.Request) {
	uiRange := r.URL.Query().Get("range")
	if uiRange == "" {
		uiRange = "3m"
	}

	var earliest sql.NullString
	if err := h.db.QueryRow(`SELECT MIN(booked_at) FROM transactions`).Scan(&earliest); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	start := startDateForRange(uiRange, earliest.String)

	points, err := h.computeNetWorthHistory(start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	respondJSON(w, struct {
		Points []ChartPoint `json:"points"`
	}{Points: points})
}

func (h *Handlers) computeNetWorthHistory(start time.Time) ([]ChartPoint, error) {
	var currentTotal float64
	if err := h.db.QueryRow(`SELECT COALESCE(SUM(balance), 0) FROM accounts`).Scan(&currentTotal); err != nil {
		return nil, err
	}

	startStr := start.Format("2006-01-02")
	rows, err := h.db.Query(
		`SELECT booked_at, kind, amount FROM transactions WHERE booked_at >= ? ORDER BY booked_at ASC`,
		startStr,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	effectByDate := map[string]float64{}
	for rows.Next() {
		var date, kind string
		var amount float64
		if err := rows.Scan(&date, &kind, &amount); err != nil {
			return nil, err
		}
		switch kind {
		case "income":
			effectByDate[date] += amount
		case "expense":
			effectByDate[date] -= amount
			// transfer: kein Effekt auf das Gesamtvermögen
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	today := time.Now()
	var dates []string
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	if len(dates) == 0 {
		dates = []string{today.Format("2006-01-02")}
	}

	points := make([]ChartPoint, len(dates))
	var futureEffect float64
	for i := len(dates) - 1; i >= 0; i-- {
		points[i] = ChartPoint{Label: dates[i], Value: currentTotal - futureEffect}
		futureEffect += effectByDate[dates[i]]
	}

	return points, nil
}

// DepotHistory liefert die Wertentwicklung des Depots (nur CHF-Positionen)
// für den angefragten Zeitraum, basierend auf historischen Kursen je Ticker.
func (h *Handlers) DepotHistory(w http.ResponseWriter, r *http.Request) {
	uiRange := r.URL.Query().Get("range")
	if uiRange == "" {
		uiRange = "3m"
	}
	yahooRange, yahooInterval := stocks.MapUIRange(uiRange)

	holdings, err := h.loadHoldings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tickers := map[string]bool{}
	for _, hd := range holdings {
		tickers[hd.Ticker] = true
	}

	type tickerResult struct {
		ticker   string
		points   []stocks.HistoryPoint
		currency string
		err      error
	}

	results := make([]tickerResult, 0, len(tickers))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for ticker := range tickers {
		wg.Add(1)
		go func(ticker string) {
			defer wg.Done()
			points, currency, err := stocks.GetHistory(ticker, yahooRange, yahooInterval)
			mu.Lock()
			results = append(results, tickerResult{ticker: ticker, points: points, currency: currency, err: err})
			mu.Unlock()
		}(ticker)
	}
	wg.Wait()

	priceByTickerLabel := map[string]map[string]float64{}
	currencyByTicker := map[string]string{}
	labelSet := map[string]bool{}
	var hasForeignCurrency bool

	for _, res := range results {
		if res.err != nil {
			continue // Ticker ohne verfügbare Historie wird einfach ausgelassen
		}
		currencyByTicker[res.ticker] = res.currency
		if res.currency != "" && res.currency != "CHF" {
			hasForeignCurrency = true
		}
		m := map[string]float64{}
		for _, p := range res.points {
			m[p.Label] = p.Close
			labelSet[p.Label] = true
		}
		priceByTickerLabel[res.ticker] = m
	}

	labels := make([]string, 0, len(labelSet))
	for l := range labelSet {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	points := make([]ChartPoint, 0, len(labels))
	for _, label := range labels {
		datePart := label
		if len(label) > 10 {
			datePart = label[:10]
		}
		var total float64
		for _, hd := range holdings {
			if currencyByTicker[hd.Ticker] != "CHF" {
				continue
			}
			if hd.PurchaseDate > datePart {
				continue
			}
			price, ok := priceByTickerLabel[hd.Ticker][label]
			if !ok {
				continue
			}
			total += hd.Quantity * price
		}
		points = append(points, ChartPoint{Label: label, Value: total})
	}

	respondJSON(w, struct {
		Points             []ChartPoint `json:"points"`
		HasForeignCurrency bool         `json:"hasForeignCurrency"`
	}{Points: points, HasForeignCurrency: hasForeignCurrency})
}

// SankeyFlow ist eine einzelne Fluss-Kante im Sankey-Diagramm.
type SankeyFlow struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Flow float64 `json:"flow"`
}

// SankeyData liefert Einnahmen-/Ausgaben-Flüsse für einen Monat
// (Kategorie -> "Einnahmen" -> Kategorie), ohne Transfers.
func (h *Handlers) SankeyData(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}

	rows, err := h.db.Query(`
		SELECT kind,
		       COALESCE(NULLIF(TRIM(category), ''),
		                CASE WHEN kind = 'income' THEN 'Sonstige Einnahmen' ELSE 'Sonstige Ausgaben' END) AS cat,
		       SUM(amount)
		FROM transactions
		WHERE kind IN ('income', 'expense') AND strftime('%Y-%m', booked_at) = ?
		GROUP BY kind, cat`, month)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var flows []SankeyFlow
	var totalIncome, totalExpense float64

	type catSum struct {
		cat string
		sum float64
	}
	var incomeCats, expenseCats []catSum

	for rows.Next() {
		var kind, cat string
		var sum float64
		if err := rows.Scan(&kind, &cat, &sum); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if kind == "income" {
			incomeCats = append(incomeCats, catSum{cat, sum})
			totalIncome += sum
		} else {
			expenseCats = append(expenseCats, catSum{cat, sum})
			totalExpense += sum
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, c := range incomeCats {
		flows = append(flows, SankeyFlow{From: c.cat, To: "Einnahmen", Flow: c.sum})
	}
	for _, c := range expenseCats {
		flows = append(flows, SankeyFlow{From: "Einnahmen", To: c.cat, Flow: c.sum})
	}
	if totalIncome > totalExpense {
		flows = append(flows, SankeyFlow{From: "Einnahmen", To: "Gespart", Flow: totalIncome - totalExpense})
	}

	respondJSON(w, struct {
		Month        string       `json:"month"`
		Flows        []SankeyFlow `json:"flows"`
		TotalIncome  float64      `json:"totalIncome"`
		TotalExpense float64      `json:"totalExpense"`
	}{Month: month, Flows: flows, TotalIncome: totalIncome, TotalExpense: totalExpense})
}
