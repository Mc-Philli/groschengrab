package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"household-app/internal/models"
	"household-app/internal/stocks"
)

// Depot zeigt alle Positionen mit live abgefragten Kursen an.
func (h *Handlers) Depot(w http.ResponseWriter, r *http.Request) {
	holdings, err := h.loadHoldings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	views := buildHoldingViews(holdings)

	var totalValueCHF float64
	var hasForeignCurrency bool
	for _, v := range views {
		if !v.PriceAvailable {
			continue
		}
		if v.Currency == "CHF" {
			totalValueCHF += v.CurrentValue
		} else {
			hasForeignCurrency = true
		}
	}

	data := struct {
		Holdings           []models.HoldingView
		TotalValueCHF      float64
		HasForeignCurrency bool
		ErrorMsg           string
		SuccessMsg         string
		Page               string
		Today              string
		CurrentUserName    string
	}{
		Holdings:           views,
		TotalValueCHF:      totalValueCHF,
		HasForeignCurrency: hasForeignCurrency,
		ErrorMsg:           r.URL.Query().Get("error"),
		SuccessMsg:         r.URL.Query().Get("success"),
		Page:               "depot",
		Today:               time.Now().Format("2006-01-02"),
		CurrentUserName:    currentUser(r).Name,
	}

	if err := h.tmpl.ExecuteTemplate(w, "depot.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildHoldingViews fragt die Kurse aller Positionen gleichzeitig ab
// (statt nacheinander), damit die Seite bei mehreren Positionen nicht
// unnötig langsam lädt.
func buildHoldingViews(holdings []models.Holding) []models.HoldingView {
	views := make([]models.HoldingView, len(holdings))

	var wg sync.WaitGroup
	for i, hd := range holdings {
		views[i] = models.HoldingView{
			ID:            hd.ID,
			Ticker:        hd.Ticker,
			Quantity:      hd.Quantity,
			PurchasePrice: hd.PurchasePrice,
			PurchaseDate:  hd.PurchaseDate,
			PurchaseValue: hd.Quantity * hd.PurchasePrice,
		}

		wg.Add(1)
		go func(i int, ticker string) {
			defer wg.Done()
			quote, err := stocks.GetQuote(ticker)
			if err != nil {
				return
			}
			views[i].PriceAvailable = true
			views[i].CurrentPrice = quote.Price
			views[i].Currency = quote.Currency
			views[i].CurrentValue = views[i].Quantity * quote.Price
			views[i].GainAbsolute = views[i].CurrentValue - views[i].PurchaseValue
			if views[i].PurchaseValue != 0 {
				views[i].GainPercent = views[i].GainAbsolute / views[i].PurchaseValue * 100
			}
		}(i, hd.Ticker)
	}
	wg.Wait()

	return views
}

func (h *Handlers) loadHoldings() ([]models.Holding, error) {
	rows, err := h.db.Query(`SELECT id, ticker, quantity, purchase_price, purchase_date FROM holdings ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Holding
	for rows.Next() {
		var hd models.Holding
		if err := rows.Scan(&hd.ID, &hd.Ticker, &hd.Quantity, &hd.PurchasePrice, &hd.PurchaseDate); err != nil {
			return nil, err
		}
		list = append(list, hd)
	}
	return list, rows.Err()
}

// CreateHolding verarbeitet das Formular "Position hinzufügen". Der Ticker
// wird nicht zwingend validiert, um bei kurzzeitigen Netzwerkproblemen keine
// gültige Eingabe zu blockieren – schlägt die Kursabfrage fehl, wird die
// Position trotzdem gespeichert, aber mit einem Warnhinweis quittiert.
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

	if _, err := h.db.Exec(
		`INSERT INTO holdings (ticker, quantity, purchase_price, purchase_date) VALUES (?, ?, ?, ?)`,
		ticker, quantity, purchasePrice, purchaseDate,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if _, err := stocks.GetQuote(ticker); err != nil {
		redirectWithMessage(w, r, "/depot", "error", fmt.Sprintf(
			"Position gespeichert, aber Kurs für %q konnte nicht abgerufen werden. Bitte Schreibweise prüfen (z. B. \"AAPL\", \"NESN.SW\", \"VWRL.SW\").", ticker,
		))
		return
	}

	redirectWithMessage(w, r, "/depot", "success", "Position hinzugefügt.")
}

// DeleteHolding entfernt eine Depot-Position.
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

	redirectWithMessage(w, r, "/depot", "success", "Position gelöscht.")
}
