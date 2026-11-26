package receipt

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func TestParseMigros(t *testing.T) {
	text := `MIGROS
Genossenschaft Migros Zürich
Bahnhofstrasse 10, 8001 Zürich
Tel. 044 123 45 67

Vollmilch 1l           1.95
Brot Halbweiss 500g    2.80
Bananen 0.834 kg       2.92
2 x Joghurt  0.75      1.50
Aktion Rabatt         -0.50
TOTAL CHF              8.67
Karte                  8.67
MwSt 2.6%   8.45       0.22
Cumulus Punkte 8
24.11.2026 17:42  Kasse 3`
	r := Parse(text, now)
	if r.Amount != 8.67 || r.Date != "2026-11-24" || r.Merchant != "Migros" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseOCRNoise(t *testing.T) {
	// Typische Fehllesungen: T0TAL, Komma, Leerzeichen, O statt 0.
	text := `Denner AG
Filiale Bern 23
Rivella rot 1.5l   2,5O
T0TAL         12. 30
Bar           20,00
Rückgeld       7,70
Datum: 20/11/26`
	r := Parse(text, now)
	if r.Amount != 12.30 || r.Date != "2026-11-20" || r.Merchant != "Denner" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseTotalNextLine(t *testing.T) {
	text := `Café Sonne
Cappuccino 2x 5.20  10.40
Gipfeli            2.60
Zu bezahlen
CHF 13.00
1'234 Gäste-Nr`
	r := Parse(text, now)
	if r.Amount != 13.00 || r.Merchant != "Café Sonne" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseFallbackLargest(t *testing.T) {
	// Kein Total-Wort lesbar: größter Betrag, aber nicht „Bar gegeben".
	text := `Kiosk am See
Zeitung 4.50
Kaffee 3.80
8.30
Bar 10.00
gültig bis 31.12.2027
05.11.2026`
	r := Parse(text, now)
	if r.Amount != 8.30 || r.Date != "2026-11-05" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseThousandsAndMwStTotal(t *testing.T) {
	text := `Interdiscount
Notebook 1'199.00
Total MwSt 8.1%  89.80
Total CHF 1'199.00`
	r := Parse(text, now)
	if r.Amount != 1199 || r.Merchant != "Interdiscount" {
		t.Fatalf("got %+v", r)
	}
}

func TestAmountsSkipsNumbersAndTimes(t *testing.T) {
	got := amounts("Art. 123.456 17:42 0.834 kg 2.92")
	if len(got) != 1 || got[0] != 2.92 {
		t.Fatalf("got %v", got)
	}
	got = amounts("2 x 0.75 1.50")
	if len(got) != 2 || got[1] != 1.50 {
		t.Fatalf("got %v", got)
	}
}

func TestMerchantPrefersLonger(t *testing.T) {
	if m := findMerchant([]string{"coop pronto", "Shop 123"}); m != "Coop Pronto" {
		t.Fatalf("got %q", m)
	}
	// „dm" darf nicht in „Admin" gefunden werden.
	if m := findMerchant([]string{"Bäckerei Admin"}); m != "Bäckerei" {
		t.Fatalf("got %q", m)
	}
}
