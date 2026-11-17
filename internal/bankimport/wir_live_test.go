package bankimport

import (
	"os"
	"testing"
)

// TestParseWIRPDFLive liest einen echten Auszug: WIR_PDF=/pfad/zum.pdf go test -run Live -v
func TestParseWIRPDFLive(t *testing.T) {
	path := os.Getenv("WIR_PDF")
	if path == "" {
		t.Skip("WIR_PDF nicht gesetzt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseWIRPDF(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range st.Entries {
		t.Logf("%s %-12s %-6v %10.2f %-13s %q", e.Date, e.Ref, e.Credit, e.Amount, e.Type, e.Description())
	}
	t.Logf("%d Buchungen %s–%s, Anfang %.2f, Ende %.2f, Abweichungen %d",
		len(st.Entries), st.From, st.To, st.OpeningBalance, st.ClosingBalance, st.BalanceMismatches)
	if st.BalanceMismatches != 0 {
		t.Errorf("%d Saldo-Abweichungen", st.BalanceMismatches)
	}
}
