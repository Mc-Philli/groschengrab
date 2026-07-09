// Package stocks fragt aktuelle Aktien-/ETF-Kurse über die öffentliche
// Yahoo-Finance-Schnittstelle ab (dieselbe, die auch die Python-Bibliothek
// yfinance im Hintergrund nutzt). Es ist kein API-Key nötig.
package stocks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Quote ist ein einzelner abgefragter Kurs.
type Quote struct {
	Price     float64
	Currency  string
	FetchedAt time.Time
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]Quote{}
	cacheTTL = 15 * time.Minute

	httpClient = &http.Client{Timeout: 8 * time.Second}
)

// GetQuote liefert den aktuellen Kurs eines Tickers (z. B. "AAPL",
// "VWRL.SW", "NESN.SW"). Ergebnisse werden 15 Minuten zwischengespeichert,
// damit nicht bei jedem Seitenaufruf erneut extern angefragt wird.
func GetQuote(ticker string) (Quote, error) {
	cacheMu.Lock()
	if q, ok := cache[ticker]; ok && time.Since(q.FetchedAt) < cacheTTL {
		cacheMu.Unlock()
		return q, nil
	}
	cacheMu.Unlock()

	q, err := fetchQuote(ticker)
	if err != nil {
		return Quote{}, err
	}

	cacheMu.Lock()
	cache[ticker] = q
	cacheMu.Unlock()

	return q, nil
}

type chartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				Currency           string  `json:"currency"`
			} `json:"meta"`
		} `json:"result"`
	} `json:"chart"`
}

func fetchQuote(ticker string) (Quote, error) {
	url := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s", ticker)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Quote{}, err
	}
	// Yahoo blockt Anfragen ohne "normalen" Browser-User-Agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; household-app/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("Kursabfrage fehlgeschlagen (Status %d)", resp.StatusCode)
	}

	var parsed chartResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Quote{}, err
	}

	if len(parsed.Chart.Result) == 0 {
		return Quote{}, fmt.Errorf("Ticker %q nicht gefunden", ticker)
	}

	meta := parsed.Chart.Result[0].Meta
	if meta.RegularMarketPrice == 0 {
		return Quote{}, fmt.Errorf("kein Kurs für %q verfügbar", ticker)
	}

	return Quote{
		Price:     meta.RegularMarketPrice,
		Currency:  meta.Currency,
		FetchedAt: time.Now(),
	}, nil
}
