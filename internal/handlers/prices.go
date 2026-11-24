package handlers

import (
	"net/http"
	"time"

	"household-app/internal/models"
	"household-app/internal/stocks"
)

var weekdaysShort = [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// marketTimeLabel beschreibt, von wann ein Kurs ist: "heute 17:30",
// "gestern 17:30", "Fr 17:30" (diese Woche) oder "14.11.2026".
func marketTimeLabel(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	today := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	switch days := int(today.Sub(day).Hours() / 24); {
	case days <= 0:
		return "heute " + t.Format("15:04")
	case days == 1:
		return "gestern " + t.Format("15:04")
	case days < 7:
		return weekdaysShort[t.Weekday()] + " " + t.Format("15:04")
	default:
		return t.Format("02.01.2006")
	}
}

// priceSummary fasst den Stand der Kurse einer Seite zusammen.
type priceSummary struct {
	FetchedAt  string // Uhrzeit der ältesten Abfrage, z. B. "20:45"
	MarketAsOf string // jüngster Börsenzeitpunkt, z. B. "heute 17:30"
}

func summarizePrices(depots []models.DepotView, now time.Time) priceSummary {
	var oldestFetch, newestMarket time.Time
	for _, d := range depots {
		for _, p := range d.Positions {
			if !p.PriceAvailable {
				continue
			}
			if oldestFetch.IsZero() || p.PriceFetchedAt.Before(oldestFetch) {
				oldestFetch = p.PriceFetchedAt
			}
			if p.PriceTime.After(newestMarket) {
				newestMarket = p.PriceTime
			}
		}
	}
	var s priceSummary
	if !oldestFetch.IsZero() {
		s.FetchedAt = oldestFetch.In(now.Location()).Format("15:04")
	}
	s.MarketAsOf = marketTimeLabel(newestMarket, now)
	return s
}

// RefreshPrices verwirft die zwischengespeicherten Kurse; die Depot-Seite
// fragt danach alles frisch ab.
func (h *Handlers) RefreshPrices(w http.ResponseWriter, r *http.Request) {
	stocks.ClearCache()
	redirectWithMessage(w, r, "/depot", "success", "Kurse aktualisiert.")
}
