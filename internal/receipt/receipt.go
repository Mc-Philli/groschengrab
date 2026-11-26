// Package receipt liest Betrag, Datum und Händler aus dem Text eines
// Kassenbons. Den Text liefert Tesseract (siehe ocr.go); die Erkennung ist
// absichtlich fehlertolerant, weil OCR auf Fotos von Thermopapier oft
// Ziffern und Satzzeichen verwechselt.
package receipt

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Result ist das, was aus einem Bon herausgelesen werden konnte. Leere
// Felder bedeuten „nicht erkannt"; der Nutzer ergänzt sie im Formular.
type Result struct {
	Amount   float64 `json:"amount"`
	Date     string  `json:"date"` // YYYY-MM-DD
	Merchant string  `json:"merchant"`
}

// amountRe findet Beträge wie 12.50, 12,50, 1'234.50 oder (OCR-typisch)
// „12. 50". Danach darf keine weitere Ziffer folgen (sonst wäre es z. B.
// eine Artikelnummer oder ein Datum).
var amountRe = regexp.MustCompile(`(\d{1,3}(?:['’]\d{3})+|\d{1,5}) ?[.,] ?(\d{2})`)

// ocrZeroRe: OCR liest auf Thermopapier gern O statt 0 in Zahlen („12.5O").
var ocrZeroRe = regexp.MustCompile(`(\d[.,]?)[oO]`)

// dateRe: 24.11.2026, 24.11.26, 24/11/2026, 24-11-2026.
var dateRe = regexp.MustCompile(`(?:^|\D)(\d{1,2})[./-](\d{1,2})[./-](\d{4}|\d{2})(?:\D|$)`)

// Schlüsselwörter für die Zeile mit dem Endbetrag, nach Vorrang. Die Liste
// ist kleingeschrieben und ohne Umlaute (siehe fold).
var totalKeywords = []string{
	"total chf", "totalchf", "total", "zu bezahlen", "zu zahlen", "gesamtbetrag",
	"gesamt", "summe", "endbetrag", "betrag chf", "montant", "totale",
}

// Zeilen mit diesen Wörtern sind nie der Endbetrag, auch wenn „Total" drin
// steht (MwSt-Aufschlüsselung, Rückgeld, Rabatte, Punkte).
var notTotal = []string{
	"mwst", "mw st", "mwst.", "ust", "tva", "iva", "netto", "brutto", "steuer", "satz",
	"ruckgeld", "retour", "zuruck", "wechselgeld", "rabatt", "aktion", "gespart",
	"punkte", "cumulus", "supercard", "zwischentotal", "zwischensumme", "subtotal",
	"anzahl", "artikel",
}

// Bekannte Händler in der Schweiz; der erste Treffer im Bon gewinnt, längere
// Namen stehen vor kürzeren (Coop Pronto vor Coop).
var merchants = []string{
	"Coop Pronto", "Coop City", "Coop Vitality", "Coop Bau+Hobby", "Coop",
	"Migrolino", "Migrol", "Migros", "Denner", "Lidl", "Aldi", "Volg", "Spar",
	"Manor", "Globus", "Jelmoli", "Landi", "Jumbo", "Hornbach", "Bauhaus", "Obi",
	"IKEA", "Interdiscount", "Media Markt", "MediaMarkt", "Fust", "Digitec", "Galaxus",
	"Brack", "Ochsner Sport", "SportXX", "Decathlon", "Dosenbach", "H&M", "Zara",
	"C&A", "Tally Weijl", "Müller", "dm", "Rossmann", "Fielmann", "Amavita",
	"Sun Store", "Dropa", "TopPharm", "Apotheke", "Drogerie", "Bäckerei",
	"Confiserie", "Sprüngli", "Brezelkönig", "Starbucks", "McDonald's", "Burger King",
	"Subway", "k kiosk", "avec", "Aperto", "Valora", "SBB", "BLS", "Shell", "BP",
	"Agrola", "Avia", "Tamoil", "Socar", "Eni", "Esso", "Ruedi Rüssel", "Aligro",
	"Prodega", "Transgourmet", "Otto's", "Pfister", "Micasa", "Do it + Garden",
	"Qualipet", "Fressnapf", "Ex Libris", "Orell Füssli", "Thalia", "Post",
}

// Parse wertet den OCR-Text eines Bons aus. now begrenzt die Datumssuche
// (keine Daten in der Zukunft).
func Parse(text string, now time.Time) Result {
	lines := splitLines(text)
	return Result{
		Amount:   findTotal(lines),
		Date:     findDate(lines, now),
		Merchant: findMerchant(lines),
	}
}

func splitLines(text string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// fold macht eine Zeile für den Schlüsselwort-Vergleich robust: klein,
// ohne Umlaute, typische OCR-Verwechslungen in Wörtern (0→o) korrigiert.
func fold(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "é", "e", "è", "e", "à", "a").Replace(s)
	// „T0TAL", „T OTAL" → „total"; nur in Buchstabenumgebung ersetzen,
	// damit Beträge unangetastet bleiben.
	b := []rune(s)
	for i, r := range b {
		if r == '0' && i > 0 && unicode.IsLetter(b[i-1]) && i+1 < len(b) && unicode.IsLetter(b[i+1]) {
			b[i] = 'o'
		}
	}
	return string(b)
}

// amounts liefert alle Beträge einer Zeile in Lesereihenfolge.
func amounts(line string) []float64 {
	line = ocrZeroRe.ReplaceAllString(line, "${1}0")
	var out []float64
	for _, m := range amountRe.FindAllStringSubmatchIndex(line, -1) {
		// Direkt davor oder danach eine weitere Ziffer/Trennzeichen: Teil
		// einer Artikelnummer, eines Datums oder einer Uhrzeit, kein Betrag.
		if m[0] > 0 && strings.ContainsAny(line[m[0]-1:m[0]], "0123456789.,'/:") {
			continue
		}
		if m[1] < len(line) && strings.ContainsAny(line[m[1]:m[1]+1], "0123456789.,/:") {
			continue
		}
		whole := strings.NewReplacer("'", "", "’", "").Replace(line[m[2]:m[3]])
		v, err := strconv.ParseFloat(whole+"."+line[m[4]:m[5]], 64)
		if err == nil && v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// findTotal sucht den Endbetrag: zuerst die Zeile mit dem stärksten
// Total-Schlüsselwort (der Betrag steht am Zeilenende oder in der nächsten
// Zeile), sonst den größten Betrag auf dem Bon.
func findTotal(lines []string) float64 {
	for _, kw := range totalKeywords {
		for i, l := range lines {
			f := fold(l)
			if !strings.Contains(f, kw) || containsAny(f, notTotal) {
				continue
			}
			if a := amounts(l); len(a) > 0 {
				return a[len(a)-1]
			}
			// „TOTAL" allein, Betrag in der Folgezeile.
			if i+1 < len(lines) {
				if a := amounts(lines[i+1]); len(a) > 0 && !containsAny(fold(lines[i+1]), notTotal) {
					return a[len(a)-1]
				}
			}
		}
	}
	var best float64
	for _, l := range lines {
		f := fold(l)
		// Bar gegeben / Rückgeld sind oft größer als der Endbetrag.
		if containsAny(f, notTotal) || strings.HasPrefix(f, "bar") || strings.Contains(f, "gegeben") {
			continue
		}
		for _, a := range amounts(l) {
			if a > best && a < 100000 {
				best = a
			}
		}
	}
	return best
}

// findDate nimmt das erste gültige Datum, das nicht in der Zukunft und nicht
// älter als zwei Jahre ist (schützt vor Fehllesungen und „gültig bis").
func findDate(lines []string, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, l := range lines {
		for _, m := range dateRe.FindAllStringSubmatch(l, -1) {
			d, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			if y < 100 {
				y += 2000
			}
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			if t.Day() != d || int(t.Month()) != mo {
				continue // z. B. 31.02.
			}
			if t.After(today) || t.Before(today.AddDate(-2, 0, 0)) {
				continue
			}
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// findMerchant sucht zuerst bekannte Händler, sonst die erste Kopfzeile, die
// wie ein Name aussieht (überwiegend Buchstaben, keine Adresse/Telefon).
func findMerchant(lines []string) string {
	for _, m := range merchants {
		re := regexp.MustCompile(`(?i)(^|[^\pL])` + regexp.QuoteMeta(m) + `($|[^\pL])`)
		for _, l := range lines {
			if re.MatchString(l) {
				return m
			}
		}
	}
	skip := []string{"quittung", "beleg", "kassenbon", "rechnung", "kasse", "willkommen",
		"herzlich", "danke", "www", "http", "@", "tel", "mwst", "uid", "che-"}
	for i, l := range lines {
		if i >= 6 {
			break
		}
		f := fold(l)
		if containsAny(f, skip) {
			continue
		}
		letters, digits := 0, 0
		for _, r := range l {
			switch {
			case unicode.IsLetter(r):
				letters++
			case unicode.IsDigit(r):
				digits++
			}
		}
		if letters >= 3 && digits <= 1 && letters*2 >= len([]rune(l)) {
			return strings.Trim(l, " .,:;-*=#")
		}
	}
	return ""
}
