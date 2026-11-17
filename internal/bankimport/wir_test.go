package bankimport

import "testing"

// Spaltenpositionen wie im echten WIR-PDF.
func row(cells ...any) []Word {
	var line []Word
	for i := 0; i+1 < len(cells); i += 2 {
		line = append(line, Word{X: float64(cells[i].(int)), S: cells[i+1].(string)})
	}
	return line
}

func TestParseWIRRows(t *testing.T) {
	header := row(32, "Datum", 82, "Auftragsnummer", 179, "Buchungstext", 529, "Währung", 611, "Gutschrift", 692, "Belastung", 789, "Saldo")
	page1 := [][]Word{
		row(140, "Muster A. oder Muster B."),
		row(30, "Kontobewegungen"),
		header,
		row(32, "05.11.2026", 82, "ZV20261105/3", 179, "Einkauf Debitkarte 03.11.2026 12:00 Lidl sagt Danke Kartennummer: 5352******1111 Betrag:", 529, "CHF", 710, "16.78", 776, "1’213.22"),
		row(179, "EUR 18.14"),
		row(32, "04.11.2026", 82, "ZV20261104/2", 179, "TWINT Belastung Beispiel, Anna, +41790000000 0400000000000001", 529, "CHF", 714, "20.00", 776, "1’230.00"),
		row(30, "Anzeige erstellt durch E-Banking. Sämtliche Informationen erfolgen ohne jegliche Gewähr.", 767, "Seite 1 von 2"),
	}
	page2 := [][]Word{
		header,
		row(32, "02.11.2026", 82, "ZV20261102/1", 179, "Dauerauftrag (2 Transaktionen)", 529, "CHF", 700, "2’850.00", 776, "1’250.00"),
		row(82, "90000000000001", 179, "1.", 196, "Vorsorge Stiftung", 529, "CHF", 706, "600.00"),
		row(196, "Referenznummer: 123"),
		row(82, "90000000000002", 179, "2.", 196, "Vermieter AG", 529, "CHF", 700, "2’250.00"),
		row(196, "Musterweg 1"),
		row(32, "01.11.2026", 82, "ZV20261101/9", 179, "Salär / Rente Arbeitgeber AG", 529, "CHF", 614, "4’000.00", 776, "4’100.00"),
		row(179, "Arbeitgeber AG"),
		row(179, "Zahlungszweck: LOHN 11/2026"),
	}

	st, err := ParseWIRRows([][][]Word{page1, page2})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 5 {
		t.Fatalf("%d Buchungen, want 5: %+v", len(st.Entries), st.Entries)
	}
	if st.BalanceMismatches != 0 || st.OpeningBalance != 100 || st.ClosingBalance != 1213.22 {
		t.Errorf("Saldo: Anfang %v, Ende %v, Abweichungen %d", st.OpeningBalance, st.ClosingBalance, st.BalanceMismatches)
	}
	if st.From != "2026-11-01" || st.To != "2026-11-05" {
		t.Errorf("Zeitraum %s–%s", st.From, st.To)
	}

	want := []struct {
		ref, desc, typ string
		amount         float64
		credit         bool
	}{
		{"ZV20261101/9", "Arbeitgeber AG – LOHN 11/2026", "Lohn", 4000, true},
		{"ZV20261102/1/1", "Vorsorge Stiftung", "Dauerauftrag", 600, false},
		{"ZV20261102/1/2", "Vermieter AG", "Dauerauftrag", 2250, false},
		{"ZV20261104/2", "Beispiel, Anna", "TWINT", 20, false},
		{"ZV20261105/3", "Lidl sagt Danke (EUR 18.14)", "Karte", 16.78, false},
	}
	for i, w := range want {
		e := st.Entries[i]
		if e.Ref != w.ref || e.Description() != w.desc || e.Type != w.typ || e.Amount != w.amount || e.Credit != w.credit {
			t.Errorf("Buchung %d = %s %q %s %v %v, want %+v", i, e.Ref, e.Description(), e.Type, e.Amount, e.Credit, w)
		}
	}
	if st.Entries[1].Date != "2026-11-02" {
		t.Errorf("Teil eines Sammelauftrags: Datum %q", st.Entries[1].Date)
	}
}

func TestParseWIRRowsNotWIR(t *testing.T) {
	if _, err := ParseWIRRows([][][]Word{{row(10, "Irgendein Dokument")}}); err != ErrNotWIR {
		t.Errorf("err = %v, want ErrNotWIR", err)
	}
}
