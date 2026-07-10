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

// HistoryPoint ist ein einzelner historischer Kurspunkt.
type HistoryPoint struct {
	Label string // "2006-01-02" bei Tages-/Wochen-/Monatsdaten, "2006-01-02 15:04" bei Intraday
	Close float64
}

var (
	historyCacheMu sync.Mutex
	historyCache   = map[string]historyCacheEntry{}
)

type historyCacheEntry struct {
	points    []HistoryPoint
	currency  string
	fetchedAt time.Time
}

// MapUIRange übersetzt eine UI-Zeitraum-Auswahl (1d, 1m, 3m, 6m, 1y, max) in
// die von Yahoo Finance erwarteten range/interval-Parameter.
func MapUIRange(uiRange string) (yahooRange, yahooInterval string) {
	switch uiRange {
	case "1d":
		return "1d", "5m"
	case "1m":
		return "1mo", "1d"
	case "3m":
		return "3mo", "1d"
	case "6m":
		return "6mo", "1d"
	case "1y":
		return "1y", "1wk"
	case "max":
		return "max", "1mo"
	default:
		return "3mo", "1d"
	}
}

// GetHistory liefert die historischen Schlusskurse eines Tickers für den
// angegebenen Yahoo-Zeitraum/-Intervall (siehe MapUIRange). Ergebnisse werden
// ebenfalls 15 Minuten zwischengespeichert.
func GetHistory(ticker, yahooRange, yahooInterval string) ([]HistoryPoint, string, error) {
	key := ticker + "|" + yahooRange + "|" + yahooInterval

	historyCacheMu.Lock()
	if e, ok := historyCache[key]; ok && time.Since(e.fetchedAt) < cacheTTL {
		historyCacheMu.Unlock()
		return e.points, e.currency, nil
	}
	historyCacheMu.Unlock()

	points, currency, err := fetchHistory(ticker, yahooRange, yahooInterval)
	if err != nil {
		return nil, "", err
	}

	historyCacheMu.Lock()
	historyCache[key] = historyCacheEntry{points: points, currency: currency, fetchedAt: time.Now()}
	historyCacheMu.Unlock()

	return points, currency, nil
}

type chartHistoryResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency string `json:"currency"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

func fetchHistory(ticker, yahooRange, yahooInterval string) ([]HistoryPoint, string, error) {
	url := fmt.Sprintf(
		"https://query1.finance.yahoo.com/v8/finance/chart/%s?range=%s&interval=%s",
		ticker, yahooRange, yahooInterval,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; household-app/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Historienabfrage fehlgeschlagen (Status %d)", resp.StatusCode)
	}

	var parsed chartHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", err
	}

	if len(parsed.Chart.Result) == 0 {
		return nil, "", fmt.Errorf("Ticker %q nicht gefunden", ticker)
	}

	result := parsed.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, "", fmt.Errorf("keine Kursdaten für %q", ticker)
	}

	intraday := yahooInterval == "5m" || yahooInterval == "15m" || yahooInterval == "30m" || yahooInterval == "1h" || yahooInterval == "60m"
	layout := "2006-01-02"
	if intraday {
		layout = "2006-01-02 15:04"
	}

	closes := result.Indicators.Quote[0].Close
	var points []HistoryPoint
	for i, ts := range result.Timestamp {
		if i >= len(closes) || closes[i] == nil {
			continue
		}
		points = append(points, HistoryPoint{
			Label: time.Unix(ts, 0).Format(layout),
			Close: *closes[i],
		})
	}

	return points, result.Meta.Currency, nil
}
