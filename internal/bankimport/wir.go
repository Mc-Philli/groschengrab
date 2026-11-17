// Package bankimport liest Kontoauszüge von Banken ein.
//
// Unterstützt wird der PDF-Export „Kontobewegungen" aus dem E-Banking der
// WIR Bank. Das PDF ist eine Tabelle (Datum, Auftragsnummer, Buchungstext,
// Währung, Gutschrift, Belastung, Saldo); ob ein Betrag eine Gutschrift oder
// eine Belastung ist, steht nur in der Spaltenposition. Deshalb wird der Text
// samt x-Koordinaten gelesen und den Spalten der Kopfzeile zugeordnet.
package bankimport

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Entry ist eine Buchung aus dem Kontoauszug.
type Entry struct {
	Date        string  // Buchungsdatum, YYYY-MM-DD
	Ref         string  // Auftragsnummer der Bank, eindeutig (bei Sammelaufträgen mit /1, /2 …)
	Type        string  // kurze Art, z. B. "TWINT", "Karte", "E-Banking", "Dauerauftrag", "Lohn"
	Payee       string  // Gegenpartei bzw. Händler
	Purpose     string  // Zahlungszweck, falls angegeben
	Foreign     string  // Originalbetrag in Fremdwährung, z. B. "EUR 35.38"
	RawText     string  // Buchungstext wie im Auszug
	Amount      float64 // immer positiv
	Credit      bool    // true = Gutschrift, false = Belastung
	Balance     float64 // Saldo nach der Buchung (0 bei Teilen eines Sammelauftrags)
	HasBalance  bool
	partOfGroup bool
}

// Description baut eine kurze Beschreibung für die Buchung in der App.
func (e Entry) Description() string {
	d := e.Payee
	if d == "" {
		d = e.RawText
	}
	if e.Purpose != "" {
		d += " – " + e.Purpose
	}
	if e.Foreign != "" {
		d += " (" + e.Foreign + ")"
	}
	return d
}

// Statement ist ein eingelesener Auszug, älteste Buchung zuerst.
type Statement struct {
	Entries        []Entry
	OpeningBalance float64 // Saldo vor der ältesten Buchung
	ClosingBalance float64 // Saldo nach der jüngsten Buchung
	From, To       string  // Zeitraum (YYYY-MM-DD)
	// BalanceMismatches zählt Buchungen, bei denen Betrag und Saldo nicht
	// zusammenpassen – ein Hinweis, dass etwas falsch gelesen wurde.
	BalanceMismatches int
}

// Word ist ein Textstück einer Zeile mit seiner x-Position (in Punkt).
type Word struct {
	X float64
	S string
}

// ParseWIRPDF liest einen WIR-Auszug aus PDF-Daten.
func ParseWIRPDF(data []byte) (Statement, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Statement{}, fmt.Errorf("PDF konnte nicht geöffnet werden: %w", err)
	}
	var pages [][][]Word
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			return Statement{}, fmt.Errorf("Seite %d konnte nicht gelesen werden: %w", i, err)
		}
		// Oben nach unten (PDF-Koordinaten wachsen nach oben).
		sort.SliceStable(rows, func(a, b int) bool { return rows[a].Position > rows[b].Position })
		var page [][]Word
		for _, row := range rows {
			var line []Word
			for _, t := range row.Content {
				if s := strings.TrimSpace(t.S); s != "" {
					line = append(line, Word{X: t.X, S: s})
				}
			}
			if len(line) > 0 {
				sort.SliceStable(line, func(a, b int) bool { return line[a].X < line[b].X })
				page = append(page, line)
			}
		}
		pages = append(pages, page)
	}
	return ParseWIRRows(pages)
}

// columns hält die x-Positionen der Spaltenköpfe einer Seite.
type columns struct {
	ref, text, currency, credit, debit, balance float64
}

var (
	dateRe     = regexp.MustCompile(`^(\d{2})\.(\d{2})\.(\d{4})$`)
	amountRe   = regexp.MustCompile(`^-?[\d'’]+\.\d{2}$`)
	subItemRe  = regexp.MustCompile(`^(\d+)\.$`)
	groupRe    = regexp.MustCompile(`^Dauerauftrag \(\d+ Transaktionen\)$`)
	foreignRe  = regexp.MustCompile(`Betrag:\s*([A-Z]{3})\s*([\d'’]+\.\d{2})`)
	longNumRe  = regexp.MustCompile(`\s+\d{10,}$`)
	phoneRe    = regexp.MustCompile(`,?\s*\+\d{9,}`)
	cardTimeRe = regexp.MustCompile(`^\d{2}\.\d{2}\.\d{4} \d{2}:\d{2} `)
)

// ErrNotWIR heißt: im PDF wurde keine Tabelle „Kontobewegungen" gefunden.
var ErrNotWIR = errors.New("keine WIR-Kontobewegungen gefunden – ist das der PDF-Export „Kontobewegungen“ aus dem WIR E-Banking?")

// ParseWIRRows wertet die Textzeilen aller Seiten aus (je Seite oben nach
// unten, je Zeile links nach rechts).
func ParseWIRRows(pages [][][]Word) (Statement, error) {
	var raw []Entry // Reihenfolge wie im Auszug: jüngste zuerst
	var cur *Entry
	var parts []Entry // Einzelzahlungen des aktuellen Sammelauftrags
	var part *Entry

	flush := func() {
		if cur == nil {
			return
		}
		if part != nil {
			parts = append(parts, *part)
			part = nil
		}
		sum := 0.0
		for _, p := range parts {
			sum += p.Amount
		}
		if len(parts) > 0 && math.Abs(sum-cur.Amount) < 0.005 {
			// Sammelauftrag: die Einzelzahlungen einzeln übernehmen, die
			// letzte trägt den Saldo der Sammelbuchung.
			for i := range parts {
				parts[i].Date = cur.Date
				parts[i].Credit = cur.Credit
				parts[i].Type = "Dauerauftrag"
				parts[i].Ref = fmt.Sprintf("%s/%d", cur.Ref, i+1)
				parts[i].partOfGroup = true
			}
			parts[len(parts)-1].Balance = cur.Balance
			parts[len(parts)-1].HasBalance = cur.HasBalance
			// Jüngste zuerst wie der Rest: die mit Saldo kommt zuerst.
			for i := len(parts) - 1; i >= 0; i-- {
				raw = append(raw, parts[i])
			}
		} else {
			raw = append(raw, *cur)
		}
		cur, parts = nil, nil
	}

	found := false
	for _, page := range pages {
		var col *columns
		for _, line := range page {
			if col == nil {
				col = headerColumns(line)
				continue
			}
			if strings.HasPrefix(line[0].S, "Anzeige erstellt durch") || strings.HasPrefix(line[0].S, "Seite ") {
				continue
			}
			found = true

			if m := dateRe.FindStringSubmatch(line[0].S); m != nil && line[0].X < col.ref-5 {
				flush()
				e := Entry{Date: m[3] + "-" + m[2] + "-" + m[1]}
				var text []string
				var amounts []Word
				for _, w := range line[1:] {
					switch {
					case w.X < col.text-5:
						e.Ref = w.S
					case w.X < col.currency-5:
						text = append(text, w.S)
					case amountRe.MatchString(w.S):
						amounts = append(amounts, w)
					}
				}
				e.RawText = strings.Join(text, " ")
				if len(amounts) == 0 {
					return Statement{}, fmt.Errorf("Buchung vom %s (%s): kein Betrag gefunden", e.Date, e.Ref)
				}
				amt, _ := parseAmount(amounts[0].S)
				e.Amount = math.Abs(amt)
				e.Credit = amounts[0].X < col.debit-5
				if len(amounts) > 1 {
					e.Balance, _ = parseAmount(amounts[len(amounts)-1].S)
					e.HasBalance = true
				}
				cur = &e
				continue
			}
			if cur == nil {
				continue
			}

			// Einzelzahlung eines Sammelauftrags: "<Nr> 1. Empfänger … CHF 600.00"
			if groupRe.MatchString(cur.RawText) && hasSubItemMarker(line) {
				if part != nil {
					parts = append(parts, *part)
				}
				p := Entry{}
				var text []string
				for _, w := range line {
					switch {
					case w.X < col.text-5 || subItemRe.MatchString(w.S):
					case w.X < col.currency-5:
						text = append(text, w.S)
					case amountRe.MatchString(w.S):
						a, _ := parseAmount(w.S)
						p.Amount = math.Abs(a)
					}
				}
				p.RawText = strings.Join(text, " ")
				p.Payee = p.RawText
				part = &p
				continue
			}

			// Folgezeile mit Details (Adresse, Referenz, Zahlungszweck, Fremdwährung).
			target := cur
			if part != nil {
				target = part
			}
			var text []string
			for _, w := range line {
				if w.X >= col.text-5 && w.X < col.currency-5 {
					text = append(text, w.S)
				}
			}
			addDetail(target, strings.Join(text, " "))
		}
	}
	flush()

	if !found || len(raw) == 0 {
		return Statement{}, ErrNotWIR
	}
	for i := range raw {
		classify(&raw[i])
	}

	// Älteste zuerst.
	st := Statement{}
	for i := len(raw) - 1; i >= 0; i-- {
		st.Entries = append(st.Entries, raw[i])
	}

	// Anfangssaldo aus der ältesten Buchung mit Saldo zurückrechnen, dann
	// prüfen: Saldo davor ± Betrag = Saldo danach.
	sum := 0.0
	for _, e := range st.Entries {
		sum += signed(e)
		if e.HasBalance {
			st.OpeningBalance = e.Balance - sum
			break
		}
	}
	running := st.OpeningBalance
	for _, e := range st.Entries {
		running += signed(e)
		if e.HasBalance && math.Abs(running-e.Balance) > 0.005 {
			st.BalanceMismatches++
			running = e.Balance
		}
	}
	st.ClosingBalance = st.OpeningBalance
	for _, e := range st.Entries {
		st.ClosingBalance += signed(e)
	}
	st.ClosingBalance = math.Round(st.ClosingBalance*100) / 100
	st.OpeningBalance = math.Round(st.OpeningBalance*100) / 100
	st.From = st.Entries[0].Date
	st.To = st.Entries[len(st.Entries)-1].Date
	return st, nil
}

func signed(e Entry) float64 {
	if e.Credit {
		return e.Amount
	}
	return -e.Amount
}

// headerColumns erkennt die Kopfzeile der Tabelle und liefert die Spalten.
func headerColumns(line []Word) *columns {
	c := columns{}
	seen := 0
	for _, w := range line {
		switch w.S {
		case "Auftragsnummer":
			c.ref = w.X
		case "Buchungstext":
			c.text = w.X
		case "Währung":
			c.currency = w.X
		case "Gutschrift":
			c.credit = w.X
		case "Belastung":
			c.debit = w.X
		case "Saldo":
			c.balance = w.X
		default:
			continue
		}
		seen++
	}
	if seen < 6 {
		return nil
	}
	return &c
}

func hasSubItemMarker(line []Word) bool {
	for _, w := range line[:min(3, len(line))] {
		if subItemRe.MatchString(w.S) {
			return true
		}
	}
	return false
}

// addDetail wertet eine Folgezeile aus.
func addDetail(e *Entry, text string) {
	if text == "" {
		return
	}
	if rest, ok := strings.CutPrefix(text, "Zahlungszweck:"); ok {
		e.Purpose = strings.TrimSpace(rest)
		return
	}
	if strings.HasPrefix(text, "Referenznummer:") {
		return
	}
	// "Betrag: EUR 18.14" kann über zwei Zeilen verteilt sein ("Betrag:" steht
	// dann schon im Buchungstext), deshalb im Gesamttext suchen.
	if strings.Contains(text, "Betrag:") || strings.Contains(e.RawText, "Betrag:") {
		if m := foreignRe.FindStringSubmatch(e.RawText + " " + text); m != nil {
			e.Foreign = m[1] + " " + strings.NewReplacer("’", "'").Replace(m[2])
			return
		}
	}
}

// classify setzt Art und Gegenpartei anhand des Buchungstexts.
func classify(e *Entry) {
	if e.partOfGroup {
		return
	}
	t := e.RawText
	if m := foreignRe.FindStringSubmatch(t); m != nil && e.Foreign == "" {
		e.Foreign = m[1] + " " + strings.NewReplacer("’", "'").Replace(m[2])
	}
	cut := func(prefix string) (string, bool) {
		rest, ok := strings.CutPrefix(t, prefix)
		return strings.TrimSpace(rest), ok
	}
	switch {
	case strings.HasPrefix(t, "TWINT "):
		e.Type = "TWINT"
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "TWINT Belastung"), "TWINT Gutschrift"))
		rest = longNumRe.ReplaceAllString(rest, "")
		rest = phoneRe.ReplaceAllString(rest, "")
		e.Payee = strings.Trim(strings.TrimSpace(rest), ", ")
	case strings.HasPrefix(t, "Einkauf Debitkarte"):
		e.Type = "Karte"
		rest, _ := cut("Einkauf Debitkarte")
		rest = cardTimeRe.ReplaceAllString(rest, "")
		if i := strings.Index(rest, "Kartennummer:"); i >= 0 {
			rest = rest[:i]
		}
		e.Payee = strings.TrimSpace(rest)
	case strings.HasPrefix(t, "Belast. E-Banking"), strings.HasPrefix(t, "Gutschr. E-Banking"):
		e.Type = "E-Banking"
		e.Payee = strings.TrimSpace(t[strings.Index(t, "E-Banking")+len("E-Banking"):])
	case strings.HasPrefix(t, "Dauerauftrag"):
		e.Type = "Dauerauftrag"
		e.Payee, _ = cut("Dauerauftrag")
	case strings.HasPrefix(t, "Salär / Rente"):
		e.Type = "Lohn"
		e.Payee, _ = cut("Salär / Rente")
	default:
		e.Type = "Bank"
		e.Payee = t
	}
	if e.Payee == "" {
		e.Payee = t
	}
}

// parseAmount liest Beträge wie "69’834.63" oder "1'000.00".
func parseAmount(s string) (float64, error) {
	s = strings.NewReplacer("’", "", "'", "").Replace(strings.TrimSpace(s))
	return strconv.ParseFloat(s, 64)
}
