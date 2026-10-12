#!/usr/bin/env python3
"""Erzeugt reproduzierbare Testdaten für die Haushalts-App.

Ausgabe (im selben Ordner wie dieses Skript):
  accounts.csv              4 Konten (Import-Format der App)
  transactions.csv          ca. 13 Monate Buchungen (Import-Format der App)
  transactions_invalid.csv  absichtlich fehlerhafte Zeilen für den Fehlertest

Zusätzlich gibt das Skript die Sollwerte als JSON aus, damit man die Anzeige
der App damit vergleichen kann.

Aufruf:  python3 generate.py
"""
import csv
import json
import random
import datetime as dt
from decimal import Decimal as D
from pathlib import Path

random.seed(42)

OUT = Path(__file__).resolve().parent
TODAY = dt.date(2026, 10, 11)
LAST_DAY = TODAY - dt.timedelta(days=1)  # keine Buchungen in der Zukunft
START = dt.date(2025, 9, 1)

ANNA = "Test-Anna"
BEN = "Test-Ben"
PRIV = "Test Privatkonto"
SPAR = "Test Sparkonto"
CASH = "Test Bargeld"
CARD = "Test Kreditkarte"


def fmt_plain(a):
    return f"{a:.2f}"


def fmt_apostrophe(a):
    return f"{a:,.2f}".replace(",", "'")


def fmt_comma(a):
    return f"{a:.2f}".replace(".", ",")


ACCOUNTS = [
    (PRIV, "Girokonto", D("2500.00"), fmt_plain),
    (SPAR, "Sparkonto", D("10000.00"), fmt_apostrophe),  # "10'000.00" testet den Parser
    (CASH, "Bar", D("150.00"), fmt_plain),
    (CARD, "Sonstiges", D("0.00"), fmt_plain),
]

rows = []


def rnd(lo, hi):
    return D(str(round(random.uniform(lo, hi), 2))).quantize(D("0.01"))


def add(date, kind, account, amount, category, description, by, to_account="", fmt=fmt_plain):
    if date > LAST_DAY:
        return
    rows.append(
        dict(
            date=date,
            kind=kind,
            account=account,
            to=to_account,
            amount=amount,
            amount_str=fmt(amount),
            category=category,
            description=description,
            by=by,
        )
    )


def months(start, end):
    y, m = start.year, start.month
    while dt.date(y, m, 1) <= end:
        yield y, m
        m += 1
        if m == 13:
            y, m = y + 1, 1


RESTAURANTS = ["Pizzeria Roma", "Sushi Bar", 'Café "Zur Linde", Zürich', "Thai Take-away"]
GROCERIES = ["Migros", "Coop", "Denner", "Aldi"]
LEISURE = ["Kino", "Schwimmbad", "Buch", "Konzert"]

for y, m in months(START, LAST_DAY):
    def d(day, y=y, m=m):
        return dt.date(y, m, day)

    # Fixe monatliche Buchungen (gemischte Betragsformate testen den Parser)
    add(d(1), "expense", PRIV, D("1850.00"), "Wohnen", "Miete", ANNA, fmt=fmt_apostrophe)
    add(d(3), "expense", PRIV, D("85.00"), "Transport", "ÖV-Abo", BEN, fmt=fmt_comma)
    add(d(5), "expense", PRIV, D("385.40"), "Versicherung", "Krankenkasse", ANNA)
    add(d(12), "transfer", PRIV, D("200.00"), "", "Bargeld abheben", BEN, to_account=CASH)
    add(d(15), "income", PRIV, D("800.00"), "Nebenjob", "Nebenjob Ben", BEN)
    add(d(25), "income", PRIV, D("6500.00"), "Lohn", "Lohn Anna", ANNA, fmt=fmt_apostrophe)
    add(d(26), "transfer", PRIV, D("500.00"), "", "Sparen", ANNA, to_account=SPAR)

    # Variable Ausgaben
    for day in sorted(random.sample(range(2, 29), random.randint(6, 8))):
        add(
            d(day), "expense", random.choice([PRIV, CARD]), rnd(18.5, 142.8),
            "Lebensmittel", random.choice(GROCERIES), random.choice([ANNA, BEN]),
        )
    for day in sorted(random.sample(range(2, 29), random.randint(2, 3))):
        add(
            d(day), "expense", CARD, rnd(25, 120),
            "Restaurant", random.choice(RESTAURANTS), random.choice([ANNA, BEN]),
        )
    for day in sorted(random.sample(range(2, 29), random.randint(1, 2))):
        add(
            d(day), "expense", random.choice([CASH, CARD]), rnd(10, 60),
            "Freizeit", random.choice(LEISURE), random.choice([ANNA, BEN]),
        )
    if m % 3 == 0:
        add(d(random.randint(5, 25)), "expense", CARD, rnd(80, 250), "Kleidung", "Kleider", ANNA)

    # Buchung ohne Kategorie (und jeden zweiten Monat ohne Beschreibung)
    add(d(random.randint(2, 28)), "expense", CASH, rnd(5, 30), "",
        "Diverses" if m % 2 == 0 else "", BEN)

    # Quartalszinsen
    if m in (3, 6, 9, 12):
        add(d(28), "income", SPAR, rnd(8, 20), "Zinsen", "Zinsgutschrift", ANNA)

# Einmaliger Bonus
add(dt.date(2025, 12, 20), "income", PRIV, D("3000.00"), "Bonus", "Weihnachtsbonus", ANNA,
    fmt=fmt_apostrophe)

# Kreditkarte jeweils am 20. des Monats vollständig begleichen
# (im aktuellen Monat noch nicht -> Konto bleibt im Minus, Randfall "negativer Saldo")
for y, m in months(START, LAST_DAY):
    day = dt.date(y, m, 20)
    if day > LAST_DAY:
        continue
    owed = sum(r["amount"] for r in rows
               if r["account"] == CARD and r["kind"] == "expense" and r["date"] <= day)
    owed -= sum(r["amount"] for r in rows
                if r["to"] == CARD and r["kind"] == "transfer" and r["date"] <= day)
    if owed > 0:
        add(day, "transfer", PRIV, owed, "", "Kreditkarte bezahlen", BEN, to_account=CARD)

rows.sort(key=lambda r: r["date"])


def parse_amount(s):
    """Spiegelt parseAmount aus handlers.go."""
    s = s.strip().replace("'", "").replace(" ", "")
    if "," in s and "." not in s:
        s = s.replace(",", ".")
    return D(s)


assert all(parse_amount(r["amount_str"]) == r["amount"] for r in rows), "Betragsformat inkonsistent"
assert all(parse_amount(fmt(s)) == s for _, _, s, fmt in ACCOUNTS)

# --- CSV-Dateien schreiben ---------------------------------------------------
with open(OUT / "accounts.csv", "w", newline="", encoding="utf-8") as f:
    w = csv.writer(f, lineterminator="\n")
    w.writerow(["name", "type", "balance"])
    for name, typ, start, fmt in ACCOUNTS:
        w.writerow([name, typ, fmt(start)])

with open(OUT / "transactions.csv", "w", newline="", encoding="utf-8") as f:
    w = csv.writer(f, lineterminator="\n")
    w.writerow(["booked_at", "kind", "account", "to_account", "amount", "category", "description", "booked_by"])
    for r in rows:
        w.writerow([r["date"].isoformat(), r["kind"], r["account"], r["to"],
                    r["amount_str"], r["category"], r["description"], r["by"]])

with open(OUT / "transactions_invalid.csv", "w", newline="", encoding="utf-8") as f:
    w = csv.writer(f, lineterminator="\n")
    w.writerow(["booked_at", "kind", "account", "to_account", "amount", "category", "description", "booked_by"])
    w.writerow(["2026-10-01", "expense", PRIV, "", "1.00", "Test", "Testzeile gültig", ANNA])          # Zeile 2: ok
    w.writerow(["2026-10-02", "expense", "Test Gibtsnicht", "", "10.00", "Test", "Konto unbekannt", ANNA])  # Zeile 3
    w.writerow(["2026-10-02", "gutschrift", PRIV, "", "10.00", "Test", "Ungültige Art", ANNA])       # Zeile 4
    w.writerow(["2026-10-02", "expense", PRIV, "", "-5.00", "Test", "Negativer Betrag", ANNA])       # Zeile 5
    w.writerow(["2026-10-02", "expense", PRIV, "", "abc", "Test", "Betrag keine Zahl", ANNA])        # Zeile 6
    w.writerow(["2026-10-02", "transfer", PRIV, "Test Gibtsnicht", "10.00", "", "Zielkonto unbekannt", ANNA])  # Zeile 7
    w.writerow(["2026-10-02", "expense", PRIV, "", "10.00"])                                         # Zeile 8: zu wenige Spalten

# --- Sollwerte berechnen -----------------------------------------------------
bal = {name: start for name, _, start, _ in ACCOUNTS}
for r in rows:
    if r["kind"] == "income":
        bal[r["account"]] += r["amount"]
    elif r["kind"] == "expense":
        bal[r["account"]] -= r["amount"]
    else:
        bal[r["account"]] -= r["amount"]
        bal[r["to"]] += r["amount"]


def month_sums(prefix):
    inc, exp = {}, {}
    for r in rows:
        if r["date"].strftime("%Y-%m") != prefix or r["kind"] == "transfer":
            continue
        if r["kind"] == "income":
            key = r["category"] or "Sonstige Einnahmen"
            inc[key] = inc.get(key, D(0)) + r["amount"]
        else:
            key = r["category"] or "Sonstige Ausgaben"
            exp[key] = exp.get(key, D(0)) + r["amount"]
    return {
        "einnahmen": {k: str(v) for k, v in sorted(inc.items())},
        "ausgaben": {k: str(v) for k, v in sorted(exp.items())},
        "total_einnahmen": str(sum(inc.values(), D(0))),
        "total_ausgaben": str(sum(exp.values(), D(0))),
        "gespart": str(max(sum(inc.values(), D(0)) - sum(exp.values(), D(0)), D(0))),
    }


start_total = sum(s for _, _, s, _ in ACCOUNTS)
first_date = rows[0]["date"]
first_day_effect = sum(
    (r["amount"] if r["kind"] == "income" else -r["amount"])
    for r in rows if r["date"] == first_date and r["kind"] != "transfer"
)

summary = {
    "zeitraum": [first_date.isoformat(), rows[-1]["date"].isoformat()],
    "anzahl_buchungen": len(rows),
    "anzahl_je_art": {k: sum(1 for r in rows if r["kind"] == k) for k in ("income", "expense", "transfer")},
    "startsalden_summe": str(start_total),
    "endsalden": {k: str(v) for k, v in bal.items()},
    "total_vermoegen_end": str(sum(bal.values(), D(0))),
    "networth_am_ersten_tag": str(start_total + first_day_effect),
    "sankey_2026-09": month_sums("2026-09"),
    "sankey_2026-10": month_sums("2026-10"),
}
print(json.dumps(summary, indent=2, ensure_ascii=False))
