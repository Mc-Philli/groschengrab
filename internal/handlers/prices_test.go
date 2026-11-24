package handlers

import (
	"testing"
	"time"

	"household-app/internal/models"
)

func TestMarketTimeLabel(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 24, 20, 45, 0, 0, loc) // Dienstag
	for _, tc := range []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, ""},
		{time.Date(2026, 11, 24, 17, 30, 0, 0, loc), "heute 17:30"},
		{time.Date(2026, 11, 23, 17, 30, 0, 0, loc), "gestern 17:30"},
		{time.Date(2026, 11, 20, 17, 30, 0, 0, loc), "Fr 17:30"},
		{time.Date(2026, 11, 14, 17, 30, 0, 0, loc), "14.11.2026"},
		// UTC-Zeit von Yahoo wird in Ortszeit angezeigt.
		{time.Date(2026, 11, 24, 16, 30, 0, 0, time.UTC), "heute 17:30"},
	} {
		if got := marketTimeLabel(tc.t, now); got != tc.want {
			t.Errorf("marketTimeLabel(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestSummarizePrices(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	now := time.Date(2026, 11, 24, 20, 45, 0, 0, loc)
	depots := []models.DepotView{{Positions: []models.PositionView{
		{PriceAvailable: true, PriceFetchedAt: now.Add(-10 * time.Minute), PriceTime: time.Date(2026, 11, 24, 17, 30, 0, 0, loc)},
		{PriceAvailable: true, PriceFetchedAt: now.Add(-2 * time.Minute), PriceTime: time.Date(2026, 11, 21, 17, 30, 0, 0, loc)},
		{PriceAvailable: false},
	}}}
	s := summarizePrices(depots, now)
	if s.FetchedAt != "20:35" || s.MarketAsOf != "heute 17:30" {
		t.Errorf("summarizePrices = %+v", s)
	}
	if s := summarizePrices(nil, now); s.FetchedAt != "" || s.MarketAsOf != "" {
		t.Errorf("ohne Kurse: %+v", s)
	}
}
