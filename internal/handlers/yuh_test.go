package handlers

import (
	"testing"

	"household-app/internal/models"
)

// Erfundene Beispieldaten im Format des Yuh-Aktivitätenexports.
const yuhSample = `DATE,ACTIVITY TYPE,ACTIVITY NAME,DEBIT,DEBIT CURRENCY,CREDIT,CREDIT CURRENCY,CARD NUMBER,LOCALITY,RECIPIENT,SENDER,FEES/COMMISSION,BUY/SELL,QUANTITY,ASSET,PRICE PER UNIT
11/04/2025,INVEST_RECURRING_ORDER_EXECUTED,"""0,5x Beispiel ETF""",-50.10,CHF,,,,,,,0.1,BUY,0.5,TEST,100.00
11/04/2025,REWARD_RECEIVED,"""Bonus""",,,,,,,,,,,5,SWQ,
11/04/2025,CARD_TRANSACTION_OUT,"""Testladen""",-24.55,CHF,,,**** 0000,"""Teststadt, CH""","""Testladen""",,0.0,,,,
11/04/2025,CARD_TRANSACTION_OUT,"""Testladen""",-24.55,CHF,,,**** 0000,"""Teststadt, CH""","""Testladen""",,0.0,,,,
12/04/2025,CARD_TRANSACTION_OUT,"""Ausland""",-10.00,EUR,,,**** 0000,,,,0.0,,,,
13/04/2025,BANK_TRANSFER_IN,"""Lohn""",,,2500.50,CHF,,,,Firma AG,,,,,
31/02/2025,CARD_TRANSACTION_OUT,"""Kaputt""",-1.00,CHF,,,,,,,,,,,
`

func TestParseYuhCSV(t *testing.T) {
	rows, problems, err := parseYuhCSV([]byte(yuhSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want 1 (ungültiges Datum)", problems)
	}
	if rows[0].Date != "2025-04-11" {
		t.Errorf("Datum = %q, want 2025-04-11 (Tag/Monat/Jahr)", rows[0].Date)
	}
	if rows[2].Name != `"Testladen"` && rows[2].Name != "Testladen" {
		t.Errorf("Name = %q", rows[2].Name)
	}
}

func TestParseYuhCSVSemicolonAndBOM(t *testing.T) {
	data := "\xEF\xBB\xBFDATE;ACTIVITY TYPE;ACTIVITY NAME;DEBIT;CREDIT\n11.04.2025;CARD_TRANSACTION_OUT;Kiosk;-1,50;\n"
	rows, problems, err := parseYuhCSV([]byte(data))
	if err != nil || len(problems) != 0 || len(rows) != 1 {
		t.Fatalf("rows=%v problems=%v err=%v", rows, problems, err)
	}
	if got := classifyYuhRow(rows[0]); got.Kind != yuhExpense || got.Amount != 1.5 {
		t.Errorf("action = %+v", got)
	}
}

func TestParseYuhCSVErrors(t *testing.T) {
	if _, _, err := parseYuhCSV([]byte("")); err == nil {
		t.Error("leere Datei sollte Fehler liefern")
	}
	if _, _, err := parseYuhCSV([]byte("a,b,c\n1,2,3\n")); err == nil {
		t.Error("fehlende Spalten sollten Fehler liefern")
	}
}

func TestClassifyYuhRow(t *testing.T) {
	rows, _, _ := parseYuhCSV([]byte(yuhSample))
	want := []struct {
		kind   yuhKind
		amount float64
	}{
		{yuhBuy, 50.10},
		{yuhSkip, 0}, // Reward ohne CHF-Betrag
		{yuhExpense, 24.55},
		{yuhExpense, 24.55},
		{yuhSkip, 0}, // EUR
		{yuhIncome, 2500.50},
	}
	for i, w := range want {
		got := classifyYuhRow(rows[i])
		if got.Kind != w.kind || got.Amount != w.amount {
			t.Errorf("Zeile %d: %+v, want kind=%v amount=%v", rows[i].Line, got, w.kind, w.amount)
		}
	}
}

func TestParseYuhNumber(t *testing.T) {
	cases := map[string]float64{
		"-24.55": -24.55, "24,55": 24.55, "1'234.50": 1234.5,
		"1,234.50": 1234.5, "1.234,50": 1234.5, "0.7139": 0.7139,
	}
	for in, want := range cases {
		got, ok, err := parseYuhNumber(in)
		if err != nil || !ok || got != want {
			t.Errorf("%q = %v %v %v, want %v", in, got, ok, err, want)
		}
	}
	if _, ok, err := parseYuhNumber(" "); ok || err != nil {
		t.Error("leer sollte ok=false ohne Fehler liefern")
	}
	if _, _, err := parseYuhNumber("abc"); err == nil {
		t.Error("abc sollte Fehler liefern")
	}
}

func TestYuhHashDuplicates(t *testing.T) {
	rows, _, _ := parseYuhCSV([]byte(yuhSample))
	k2, k3 := yuhRowKey(rows[2]), yuhRowKey(rows[3])
	if k2 != k3 {
		t.Fatal("identische Zeilen sollten denselben Schlüssel haben")
	}
	if yuhHash(k2, 1) == yuhHash(k3, 2) {
		t.Error("zweites Vorkommen braucht anderen Hash")
	}
	if yuhHash(k2, 1) != yuhHash(k2, 1) {
		t.Error("Hash muss stabil sein")
	}
}

func TestCategorizeYuh(t *testing.T) {
	rules := []models.CategoryRule{{Keyword: "testladen", Category: "Lebensmittel"}}
	rows, _, _ := parseYuhCSV([]byte(yuhSample))
	if got := categorizeYuh(rules, rows[2]); got != "Lebensmittel" {
		t.Errorf("Regel: %q", got)
	}
	if got := categorizeYuh(nil, rows[1]); got != "Bonus" {
		t.Errorf("Reward: %q", got)
	}
	if got := yuhDescription(rows[2]); got != "Testladen (Teststadt, CH)" {
		t.Errorf("Beschreibung: %q", got)
	}
}
