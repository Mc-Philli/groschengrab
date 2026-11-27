package handlers

import (
	"testing"
)

func TestEasterSunday(t *testing.T) {
	for year, want := range map[int]string{2024: "2024-03-31", 2026: "2026-04-05", 2027: "2027-03-28", 2030: "2030-04-21"} {
		if got := easterSunday(year).Format("2006-01-02"); got != want {
			t.Errorf("Ostern %d = %s, want %s", year, got, want)
		}
	}
}

func TestNextBusinessDay(t *testing.T) {
	cases := map[string]string{
		"2026-11-25": "2026-11-25", // Mittwoch bleibt
		"2026-11-28": "2026-11-30", // Samstag → Montag
		"2026-11-29": "2026-11-30", // Sonntag → Montag
		"2026-12-24": "2026-12-28", // Heiligabend, Weihnachten, Stephanstag, Wochenende
		"2027-01-01": "2027-01-04", // Neujahr (Fr), Berchtoldstag (Sa), So
		"2026-04-03": "2026-04-07", // Karfreitag … Ostermontag
		"2026-05-14": "2026-05-15", // Auffahrt
		"2026-05-25": "2026-05-26", // Pfingstmontag
		"2026-08-01": "2026-08-03", // Bundesfeier (Sa)
		"kaputt":     "kaputt",
	}
	for in, want := range cases {
		if got := nextBusinessDay(in); got != want {
			t.Errorf("nextBusinessDay(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestRecurringWeekendBookedOnMonday(t *testing.T) {
	h := newDepotTestHandlers(t)
	var giro int64
	h.db.QueryRow(`SELECT id FROM accounts WHERE name = 'Privatkonto'`).Scan(&giro)
	// Der 28.11.2026 ist ein Samstag.
	mustExec(t, h, `INSERT INTO recurring_transactions (account_id, user_id, kind, amount, description, start_date)
		VALUES (?, 1, 'expense', 100, 'Abo', '2026-10-28')`, giro)

	if n, _ := h.RunDueRecurring("2026-11-29"); n != 1 { // nur Oktober (Mi)
		t.Fatalf("Sonntag bucht %d, want 1", n)
	}
	list, _ := h.loadRecurring()
	if list[0].NextDue != "2026-11-30" {
		t.Errorf("nächster Termin = %s, want 2026-11-30 (Montag)", list[0].NextDue)
	}
	if n, _ := h.RunDueRecurring("2026-11-30"); n != 1 {
		t.Fatalf("Montag bucht %d, want 1", n)
	}
	var dates []string
	rows, _ := h.db.Query(`SELECT booked_at FROM transactions ORDER BY booked_at`)
	for rows.Next() {
		var d string
		rows.Scan(&d)
		dates = append(dates, d[:10])
	}
	rows.Close()
	if len(dates) != 2 || dates[0] != "2026-10-28" || dates[1] != "2026-11-30" {
		t.Errorf("Buchungsdaten %v, want [2026-10-28 2026-11-30]", dates)
	}
	// Der Plan bleibt beim 28.: Dezember ist wieder am 28. (Montag).
	list, _ = h.loadRecurring()
	if list[0].LastExecutedOn != "2026-11-28" || list[0].NextDue != "2026-12-28" {
		t.Errorf("last %s next %s", list[0].LastExecutedOn, list[0].NextDue)
	}

}
