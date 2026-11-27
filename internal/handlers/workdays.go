package handlers

import "time"

// Daueraufträge und Sparpläne werden wie bei der Bank am nächsten Werktag
// gebucht, wenn ihr Termin auf ein Wochenende oder einen Bankfeiertag fällt.
// Der eigentliche Termin (last_executed_on) bleibt der Planungsstand, nur das
// Buchungsdatum verschiebt sich.

// easterSunday berechnet den Ostersonntag (gregorianisch, Gauß/Anonymus).
func easterSunday(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// isBankHoliday: Feiertage, an denen Schweizer Banken (SIX-Kalender)
// geschlossen haben.
func isBankHoliday(t time.Time) bool {
	switch {
	case t.Month() == time.January && (t.Day() == 1 || t.Day() == 2), // Neujahr, Berchtoldstag
		t.Month() == time.August && t.Day() == 1, // Bundesfeier
		t.Month() == time.December && (t.Day() == 24 || t.Day() == 25 || t.Day() == 26 || t.Day() == 31):
		return true
	}
	easter := easterSunday(t.Year())
	for _, off := range []int{-2, 1, 39, 50} { // Karfreitag, Ostermontag, Auffahrt, Pfingstmontag
		if t.Equal(easter.AddDate(0, 0, off)) {
			return true
		}
	}
	return false
}

// nextBusinessDay gibt das Datum selbst zurück, wenn es ein Werktag ist,
// sonst den nächsten Werktag. Format jeweils YYYY-MM-DD; ungültige Eingaben
// kommen unverändert zurück.
func nextBusinessDay(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	for i := 0; i < 10; i++ {
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && !isBankHoliday(t) {
			break
		}
		t = t.AddDate(0, 0, 1)
	}
	return t.Format("2006-01-02")
}
