package handlers

import (
	"fmt"
	"time"

	"household-app/internal/stocks"
)

// marketData kapselt alle Kurs- und Wechselkursabfragen. Im Betrieb kommt
// alles von Yahoo Finance; Tests setzen eine Attrappe ein.
type marketData interface {
	Quote(ticker string) (stocks.Quote, error)
	CHFRate(currency string) (float64, error)
	// Daily liefert Tagesschlusskurse ab (spätestens) from.
	Daily(ticker string, from time.Time) (stocks.Series, string, error)
	// CHFRateDaily liefert den Umrechnungsfaktor nach CHF als Tagesreihe;
	// bei CHF ist ok=false und constant der feste Faktor.
	CHFRateDaily(currency string, from time.Time) (s stocks.Series, constant float64, ok bool, err error)
}

type yahooMarket struct{}

func (yahooMarket) Quote(t string) (stocks.Quote, error) { return stocks.GetQuote(t) }
func (yahooMarket) CHFRate(c string) (float64, error)    { return stocks.ToCHFRate(c) }
func (yahooMarket) Daily(t string, from time.Time) (stocks.Series, string, error) {
	return stocks.DailyCloses(t, from)
}
func (yahooMarket) CHFRateDaily(c string, from time.Time) (stocks.Series, float64, bool, error) {
	return stocks.CHFRateDaily(c, from)
}

// rateAt wertet eine CHF-Faktor-Reihe aus (siehe CHFRateDaily).
func rateAt(s stocks.Series, constant float64, ok bool, label string) (float64, bool) {
	if !ok {
		return constant, true
	}
	return s.At(label)
}

// priceOn liefert den Schlusskurs am oder vor date (YYYY-MM-DD) samt
// Notierungswährung und CHF-Faktor zu diesem Tag.
func priceOn(m marketData, ticker, date string) (price float64, currency string, chfRate float64, err error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, "", 0, err
	}
	from := d.AddDate(0, 0, -10) // etwas Vorlauf für Wochenenden/Feiertage
	series, cur, err := m.Daily(ticker, from)
	if err != nil {
		return 0, "", 0, fmt.Errorf("Kurs für %s nicht verfügbar: %w", ticker, err)
	}
	price, ok := series.At(date)
	if !ok || price <= 0 {
		return 0, "", 0, fmt.Errorf("kein Kurs für %s am %s", ticker, date)
	}
	rs, constant, isSeries, err := m.CHFRateDaily(cur, from)
	if err != nil {
		return 0, "", 0, err
	}
	rate, ok := rateAt(rs, constant, isSeries, date)
	if !ok || rate <= 0 {
		return 0, "", 0, fmt.Errorf("kein Wechselkurs %s/CHF am %s", cur, date)
	}
	return price, cur, rate, nil
}
