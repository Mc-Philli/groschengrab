package stocks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// NormalizeCurrency behandelt die Unterwährungen, in denen Yahoo manche
// Börsen notiert (z. B. London in Pence "GBp"). Zurück kommen die
// Hauptwährung und der Faktor, mit dem ein Kurs in sie umgerechnet wird.
func NormalizeCurrency(cur string) (base string, factor float64) {
	switch cur {
	case "GBp", "GBX":
		return "GBP", 0.01
	case "ZAc":
		return "ZAR", 0.01
	case "ILA":
		return "ILS", 0.01
	case "":
		return "CHF", 1
	}
	return strings.ToUpper(cur), 1
}

func fxTicker(base string) string { return base + "CHF=X" }

// ToCHFRate liefert den aktuellen Faktor, mit dem ein Betrag in der
// angegebenen (Yahoo-)Währung in CHF umgerechnet wird.
func ToCHFRate(cur string) (float64, error) {
	base, factor := NormalizeCurrency(cur)
	if base == "CHF" {
		return factor, nil
	}
	q, err := GetQuote(fxTicker(base))
	if err != nil {
		return 0, fmt.Errorf("Wechselkurs %s/CHF nicht verfügbar: %w", base, err)
	}
	return q.Price * factor, nil
}

// Series ist eine nach Label (Datum bzw. Datum+Uhrzeit) sortierte Kursreihe.
type Series struct {
	Labels []string
	Values []float64
}

func seriesFrom(points []HistoryPoint) Series {
	sort.SliceStable(points, func(i, j int) bool { return points[i].Label < points[j].Label })
	s := Series{}
	for _, p := range points {
		s.Labels = append(s.Labels, p.Label)
		s.Values = append(s.Values, p.Close)
	}
	return s
}

// At liefert den letzten Wert am oder vor label (z. B. Freitagskurs für einen
// Samstag). ok ist false, wenn label vor dem ersten bekannten Wert liegt.
func (s Series) At(label string) (float64, bool) {
	k := sort.SearchStrings(s.Labels, label)
	if k < len(s.Labels) && s.Labels[k] == label {
		return s.Values[k], true
	}
	if k == 0 {
		return 0, false
	}
	return s.Values[k-1], true
}

// rangeCovering wählt den kleinsten Yahoo-Zeitraum, der bis from zurückreicht.
func rangeCovering(from time.Time) string {
	days := time.Since(from).Hours() / 24
	switch {
	case days <= 25:
		return "1mo"
	case days <= 85:
		return "3mo"
	case days <= 175:
		return "6mo"
	case days <= 360:
		return "1y"
	case days <= 725:
		return "2y"
	case days <= 1820:
		return "5y"
	case days <= 3640:
		return "10y"
	}
	return "max"
}

// DailyCloses liefert die Tagesschlusskurse eines Tickers ab (spätestens)
// from bis heute, samt Notierungswährung.
func DailyCloses(ticker string, from time.Time) (Series, string, error) {
	points, cur, err := GetHistory(ticker, rangeCovering(from), "1d")
	if err != nil {
		return Series{}, "", err
	}
	return seriesFrom(append([]HistoryPoint(nil), points...)), cur, nil
}

// SeriesFor lädt eine Kursreihe für einen Yahoo-Zeitraum/-Intervall.
func SeriesFor(ticker, yahooRange, yahooInterval string) (Series, string, error) {
	points, cur, err := GetHistory(ticker, yahooRange, yahooInterval)
	if err != nil {
		return Series{}, "", err
	}
	return seriesFrom(append([]HistoryPoint(nil), points...)), cur, nil
}

// CHFRateSeries liefert den Umrechnungsfaktor nach CHF im Zeitverlauf. Für
// CHF selbst ist ok=false und constant der (Unterwährungs-)Faktor.
func CHFRateSeries(cur, yahooRange, yahooInterval string) (s Series, constant float64, ok bool, err error) {
	base, factor := NormalizeCurrency(cur)
	if base == "CHF" {
		return Series{}, factor, false, nil
	}
	s, _, err = SeriesFor(fxTicker(base), yahooRange, yahooInterval)
	if err != nil {
		return Series{}, 0, false, err
	}
	for i := range s.Values {
		s.Values[i] *= factor
	}
	return s, 0, true, nil
}

// CHFRateDaily ist wie CHFRateSeries, aber mit Tageswerten ab from.
func CHFRateDaily(cur string, from time.Time) (s Series, constant float64, ok bool, err error) {
	return CHFRateSeries(cur, rangeCovering(from), "1d")
}

// SearchResult ist ein Treffer der Yahoo-Suche (Ticker zu ISIN oder Name).
type SearchResult struct {
	Symbol   string
	Name     string
	Exchange string
	Type     string
}

type searchResponse struct {
	Quotes []struct {
		Symbol    string `json:"symbol"`
		ShortName string `json:"shortname"`
		LongName  string `json:"longname"`
		ExchDisp  string `json:"exchDisp"`
		TypeDisp  string `json:"typeDisp"`
		QuoteType string `json:"quoteType"`
	} `json:"quotes"`
}

// Search sucht Ticker anhand einer ISIN, eines Namens oder eines Kürzels.
// Schweizer Börsenplätze (SIX, Suffix .SW) werden zuerst gelistet.
func Search(query string) ([]SearchResult, error) {
	u := "https://query1.finance.yahoo.com/v1/finance/search?quotesCount=10&newsCount=0&q=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; household-app/1.0)")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Suche fehlgeschlagen (Status %d)", resp.StatusCode)
	}
	var parsed searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, q := range parsed.Quotes {
		if q.Symbol == "" {
			continue
		}
		name := q.LongName
		if name == "" {
			name = q.ShortName
		}
		typ := q.TypeDisp
		if typ == "" {
			typ = q.QuoteType
		}
		results = append(results, SearchResult{Symbol: q.Symbol, Name: name, Exchange: q.ExchDisp, Type: typ})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return strings.HasSuffix(results[i].Symbol, ".SW") && !strings.HasSuffix(results[j].Symbol, ".SW")
	})
	return results, nil
}
