package handlers

import (
	"fmt"
	"math"
	"testing"
	"time"

	"household-app/internal/models"
	"household-app/internal/stocks"
)

// fakeMarket liefert feste Kurse: jeder Ticker hat einen konstanten Kurs ab
// 2020-01-01, jede Währung einen konstanten CHF-Faktor.
type fakeMarket struct {
	prices map[string]float64
	cur    map[string]string
	fx     map[string]float64
}

func (f fakeMarket) Quote(t string) (stocks.Quote, error) {
	p, ok := f.prices[t]
	if !ok {
		return stocks.Quote{}, fmt.Errorf("unbekannt: %s", t)
	}
	return stocks.Quote{Price: p, Currency: f.cur[t]}, nil
}

func (f fakeMarket) CHFRate(c string) (float64, error) {
	if c == "CHF" {
		return 1, nil
	}
	r, ok := f.fx[c]
	if !ok {
		return 0, fmt.Errorf("kein Kurs %s", c)
	}
	return r, nil
}

func (f fakeMarket) Daily(t string, from time.Time) (stocks.Series, string, error) {
	p, ok := f.prices[t]
	if !ok {
		return stocks.Series{}, "", fmt.Errorf("unbekannt: %s", t)
	}
	return stocks.Series{Labels: []string{"2020-01-01"}, Values: []float64{p}}, f.cur[t], nil
}

func (f fakeMarket) CHFRateDaily(c string, from time.Time) (stocks.Series, float64, bool, error) {
	if c == "CHF" {
		return stocks.Series{}, 1, false, nil
	}
	r, err := f.CHFRate(c)
	if err != nil {
		return stocks.Series{}, 0, false, err
	}
	return stocks.Series{Labels: []string{"2020-01-01"}, Values: []float64{r}}, 0, true, nil
}

func newDepotTestHandlers(t *testing.T) *Handlers {
	h := newTestHandlers(t)
	h.market = fakeMarket{
		prices: map[string]float64{"CHFETF.SW": 100, "USDETF": 50},
		cur:    map[string]string{"CHFETF.SW": "CHF", "USDETF": "USD"},
		fx:     map[string]float64{"USD": 0.8},
	}
	if _, err := h.db.Exec(`INSERT INTO users (id, name, password_hash) VALUES (1, 'Tester', 'x')`); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNextExecutionDate(t *testing.T) {
	cases := []struct {
		start, last string
		day         int
		until, want string
	}{
		{"2026-09-01", "", 5, "2026-12-31", "2026-09-05"},
		{"2026-09-10", "", 5, "2026-12-31", "2026-10-05"},
		{"2026-09-01", "2026-09-05", 5, "2026-12-31", "2026-10-05"},
		{"2026-12-01", "2026-12-05", 5, "2027-12-31", "2027-01-05"},
		{"2026-09-01", "2026-09-05", 5, "2026-10-04", ""},
		{"2026-09-05", "", 5, "2026-09-05", "2026-09-05"},
	}
	for _, c := range cases {
		p := models.SavingsPlan{StartDate: c.start, LastExecutedOn: c.last, DayOfMonth: c.day}
		if got := nextExecutionDate(p, c.until); got != c.want {
			t.Errorf("start %s last %q until %s: got %q, want %q", c.start, c.last, c.until, got, c.want)
		}
	}
}

func TestSavingsPlanCatchUpAndFX(t *testing.T) {
	h := newDepotTestHandlers(t)
	mustExec(t, h, `INSERT INTO accounts (id, name, type, balance) VALUES (99, 'Lohnkonto', 'Girokonto', 5000)`)
	mustExec(t, h, `INSERT INTO savings_plans (id, depot, amount, day_of_month, start_date, account_id, user_id) VALUES (1, 'WillBe', 500, 5, '2026-09-01', 99, 1)`)
	mustExec(t, h, `INSERT INTO savings_plan_items (plan_id, ticker, weight) VALUES (1, 'CHFETF.SW', 60), (1, 'USDETF', 40)`)

	n, errs := h.RunDueSavingsPlans("2026-11-13")
	if len(errs) > 0 || n != 3 {
		t.Fatalf("executed %d, errs %v; want 3 Ausführungen (Sep, Okt, Nov)", n, errs)
	}
	// Zweiter Lauf am selben Tag darf nichts doppelt buchen.
	if n, _ := h.RunDueSavingsPlans("2026-11-13"); n != 0 {
		t.Fatalf("zweiter Lauf: %d Ausführungen, want 0", n)
	}

	holdings, _ := h.loadHoldings()
	if len(holdings) != 6 {
		t.Fatalf("%d Tranchen, want 6", len(holdings))
	}
	depots, total, _, missing := h.buildDepotViews(holdings)
	if len(depots) != 1 || depots[0].Name != "WillBe" || missing != 0 {
		t.Fatalf("depots = %+v, missing %d", depots, missing)
	}
	qty := map[string]float64{}
	for _, p := range depots[0].Positions {
		qty[p.Ticker] = p.Quantity
	}
	// 300 CHF / 100 = 3 Stück; 200 CHF / (50 USD × 0.8) = 5 Stück – je Monat.
	if qty["CHFETF.SW"] != 9 || qty["USDETF"] != 15 {
		t.Errorf("Anzahl = %v, want CHFETF.SW 9, USDETF 15", qty)
	}
	if math.Abs(total-1500) > 0.001 {
		t.Errorf("Depotwert = %v CHF, want 1500", total)
	}

	var balance float64
	h.db.QueryRow(`SELECT balance FROM accounts WHERE id = 99`).Scan(&balance)
	if balance != 3500 {
		t.Errorf("Kontosaldo = %v, want 3500 (3 × 500 belastet)", balance)
	}

	// Vermögensverlauf: vor dem ersten Kauf 0 Depot, danach 500 je Monat.
	vals, err := h.depotValuesByDate([]string{"2026-09-04", "2026-09-05", "2026-10-20", "2026-11-13"})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, 500, 1000, 1500}
	for i := range want {
		if math.Abs(vals[i]-want[i]) > 0.001 {
			t.Errorf("Depot am Tag %d = %v, want %v", i, vals[i], want[i])
		}
	}
}

func TestSavingsPlanUnknownTickerKeepsNothing(t *testing.T) {
	h := newDepotTestHandlers(t)
	mustExec(t, h, `INSERT INTO savings_plans (id, depot, amount, day_of_month, start_date, user_id) VALUES (1, 'Saxo', 100, 1, '2026-11-01', 1)`)
	mustExec(t, h, `INSERT INTO savings_plan_items (plan_id, ticker, weight) VALUES (1, 'CHFETF.SW', 50), (1, 'GIBTSNICHT', 50)`)

	n, errs := h.RunDueSavingsPlans("2026-11-13")
	if n != 0 || len(errs) != 1 {
		t.Fatalf("n=%d errs=%v, want 0 und 1 Fehler", n, errs)
	}
	holdings, _ := h.loadHoldings()
	if len(holdings) != 0 {
		t.Errorf("%d Tranchen gebucht, want 0 (alles oder nichts)", len(holdings))
	}
	var lastErr string
	h.db.QueryRow(`SELECT COALESCE(last_error, '') FROM savings_plans WHERE id = 1`).Scan(&lastErr)
	if lastErr == "" {
		t.Error("last_error wurde nicht gesetzt")
	}
}

func mustExec(t *testing.T, h *Handlers, q string, args ...any) {
	t.Helper()
	if _, err := h.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
