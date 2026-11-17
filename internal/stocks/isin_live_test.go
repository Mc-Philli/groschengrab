package stocks

import (
	"os"
	"testing"
)

// Läuft nur mit LIVE=1 (braucht Internet).
func TestSearchISINLive(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("LIVE=1 für Netzwerktest setzen")
	}
	res, err := SearchISIN("LU0446734104")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		t.Log(r.Symbol, r.Name, r.Exchange)
	}
	if len(res) == 0 || res[0].Symbol != "EUREUA.SW" {
		t.Errorf("erster Treffer %v, want EUREUA.SW", res)
	}
}

func TestIsISIN(t *testing.T) {
	for q, want := range map[string]bool{"LU0446734104": true, "ie00bk5bqt80": true, "VWRL.SW": false, "LU04467341": false} {
		if IsISIN(q) != want {
			t.Errorf("IsISIN(%q) != %v", q, want)
		}
	}
}
