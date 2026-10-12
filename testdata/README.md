# Testdaten für die Haushalts-App

Diese Daten sind frei erfunden und ausschließlich zum Testen gedacht. Alle Namen beginnen mit `Test`, damit sie sich von echten Daten unterscheiden lassen.

## Wichtig: nicht in die echte Datenbank importieren

Die App kann Buchungen einzeln löschen, aber nicht alle auf einmal. 279 Testbuchungen in der echten Datenbank wieder wegzuräumen wäre mühsam. Deshalb läuft der Test in einer **zweiten Instanz mit eigener Datenbank**.

Auf dem Pi (die laufende App bleibt unberührt):

```bash
cd /opt/household-app
APP_DB_PATH=/tmp/test.db APP_ADDR=:8081 ./household-app
```

Danach im Browser `http://<pi-ip>:8081` öffnen. Mit `Strg+C` beenden, die Datei `/tmp/test.db` danach löschen. Dafür braucht `main.go` die Umgebungsvariable `APP_ADDR` (ist in diesem Stand enthalten).

## Dateien

| Datei | Zweck |
|---|---|
| `accounts.csv` | 4 Konten, Startsaldo gesamt CHF 12'650.00 |
| `transactions.csv` | 279 Buchungen von 2025-09-01 bis 2026-10-10 |
| `transactions_invalid.csv` | 7 Zeilen für den Fehlertest (6 davon fehlerhaft) |
| `generate.py` | erzeugt alles neu (`python3 generate.py`), gibt die Sollwerte aus |

## Ablauf

1. In der Test-Instanz unter `/register` einen Nutzer anlegen, z. B. `Tester`.
2. Einstellungen → Konten importieren → `accounts.csv`.
3. Einstellungen → Buchungen importieren → `transactions.csv`. Erwartet: **279 Buchung(en) importiert**, keine Fehlermeldung.
4. Prüfen (siehe unten).
5. **Zuletzt** `transactions_invalid.csv` importieren (verändert die Oktober-Werte um CHF 1.00, deshalb erst nach den Prüfungen).

Beide Dateien nur **einmal** importieren. Beim zweiten Mal entstehen doppelte Konten, und die Zuordnung über den Namen wird unklar.

## Sollwerte nach dem Import von accounts.csv und transactions.csv

### Kontosalden

| Konto | Soll |
|---|---|
| Test Privatkonto | CHF 48'102.69 |
| Test Sparkonto | CHF 16'570.71 |
| Test Bargeld | CHF 2'136.03 |
| Test Kreditkarte | CHF -207.58 (rot, negativ) |
| **Total Vermögen** | **CHF 66'601.85** |

Die Summe stimmt, weil sich die Transfers gegenseitig aufheben. Das Bargeld ist hoch, weil im Datensatz monatlich CHF 200 abgehoben, aber nur wenig bar ausgegeben wird.

### Buchungen

- Insgesamt 279: 32 Einnahmen, 208 Ausgaben, 39 Transfers.
- Die Liste „Letzte Buchungen" zeigt nur die neuesten 50. Das ist so gewollt.
- Bei Transfers steht in der Spalte Konto „Quelle → Ziel".

### Vermögensverlauf

- **Max** beginnt am 2025-09-01 bei **CHF 10'800.00** (Startsalden 12'650 minus Miete 1'850 am ersten Tag).
- Der letzte Punkt entspricht dem Total Vermögen (CHF 66'601.85).
- Die Kurve steigt über die Zeit, mit kleinen Sprüngen um den 25. (Lohn).
- **1T** zeigt nur 1 bis 2 Punkte, weil Buchungen kein Uhrzeit-Feld haben.

### Sankey

| Monat | Einnahmen | Ausgaben | Gespart |
|---|---|---|---|
| **September 2026** | 7'315.14 (Lohn 6'500.00, Nebenjob 800.00, Zinsen 15.14) | 3'137.84 | 4'177.30 |
| **Oktober 2026** | 0.00 | 2'548.16 | 0 |

Ausgaben September: Wohnen 1'850.00, Lebensmittel 488.46, Versicherung 385.40, Restaurant 118.59, Freizeit 93.43, Kleidung 92.14, Transport 85.00, Sonstige Ausgaben 24.82 (die Buchung ohne Kategorie).

Ausgaben Oktober (bis zum 10.): Wohnen 1'850.00, Lebensmittel 166.45, Versicherung 385.40, Transport 85.00, Freizeit 61.31.

Oktober ist ein Randfall: Es gibt Ausgaben, aber noch keine Einnahmen. Das Diagramm sollte nicht abstürzen. Ob es sinnvoll aussieht, ist Teil des Tests.

## Randfälle in den Daten

- Betragsformate gemischt: `1'850.00` (Apostroph), `85,00` (Komma, in der CSV in Anführungszeichen), `91.90` (Punkt). Alle müssen gleich eingelesen werden. Das Sparkonto startet mit `10'000.00`.
- Beschreibung mit Komma, Anführungszeichen und Umlauten: `Café "Zur Linde", Zürich`.
- Buchungen ohne Kategorie (landen im Sankey unter „Sonstige Ausgaben") und ohne Beschreibung.
- Mehrere Buchungen am selben Tag.
- Kreditkarte mit negativem Saldo.
- Zwei Nutzer in den Buchungen (`Test-Anna`, `Test-Ben`). Beim Import werden sie ohne Passwort angelegt. Das ist normal, anmelden kannst du dich mit dem Nutzer aus Schritt 1.

## Fehlertest mit transactions_invalid.csv

Erwartet wird eine rote Meldung, die mit **„1 Buchung(en) importiert."** beginnt, danach „Übersprungen:" mit den ersten fünf Problemen und dem Hinweis „…weitere Fehler gekürzt":

| Zeile | Fehler |
|---|---|
| 2 | gültig (wird importiert, Privatkonto -1.00, Kategorie „Test") |
| 3 | Konto „Test Gibtsnicht" nicht gefunden |
| 4 | ungültige Buchungsart „gutschrift" |
| 5 | ungültiger Betrag „-5.00" |
| 6 | ungültiger Betrag „abc" |
| 7 | Zielkonto „Test Gibtsnicht" nicht gefunden |
| 8 | erwartet 8 Spalten, gefunden 5 (wird wegen der Kürzung nicht mehr angezeigt) |

Danach darf sich außer der einen gültigen Buchung nichts verändert haben: Privatkonto CHF 48'101.69.

## Checkliste je Funktion

**Login**
- [ ] Registrieren, abmelden, mit falschem Passwort anmelden (Fehlermeldung), mit richtigem anmelden
- [ ] Seiten ohne Login aufrufen führt auf `/login`

**Buchungen**
- [ ] Neue Ausgabe: Saldo sinkt um den Betrag, Datum ist heute vorausgefüllt
- [ ] Neue Einnahme: Saldo steigt
- [ ] Transfer: Quelle sinkt, Ziel steigt, Total Vermögen bleibt gleich
- [ ] Transfer mit gleichem Quell- und Zielkonto wird abgelehnt
- [ ] Buchung löschen (Ausgabe, Einnahme, Transfer): Salden werden korrekt zurückgerechnet
- [ ] Betrag `1'234.50`, `12,50`, `0`, `-5` und `abc` ausprobieren

**Konten**
- [ ] Konto mit Buchungen löschen wird verweigert (Meldung mit Anzahl)
- [ ] Konto ohne Buchungen löschen funktioniert
- [ ] Ein Konto, das nur als Transfer-Ziel vorkommt, lässt sich ebenfalls nicht löschen

**Kategorien**
- [ ] Anlegen, doppelt anlegen (Fehlermeldung), löschen
- [ ] Gelöschte Kategorie verschwindet aus der Auswahl, alte Buchungen behalten den Text

**Export/Import**
- [ ] Buchungen exportieren, die Datei in Excel/Numbers öffnen (Umlaute korrekt?)
- [ ] Export in eine leere Test-Datenbank wieder importieren: gleiche Salden wie vorher

**Depot (von Hand eintragen, der CSV-Import kennt keine Depot-Positionen)**
- [ ] `NESN.SW`, Anzahl 10, Kaufpreis 100.00: Kurs in CHF, Gewinn/Verlust sichtbar
- [ ] `AAPL`, Anzahl 5, Kaufpreis 150.00: Kurs in USD, taucht nicht im CHF-Depotwert auf, Hinweistext erscheint
- [ ] `VWRL.SW`, Anzahl 20, Kaufpreis 90.00
- [ ] `XXXXNOTREAL`: Position wird gespeichert, aber mit Warnmeldung zum Kurs, in der Tabelle „Kurs nicht verfügbar"
- [ ] Depot-Diagramm für alle Zeiträume durchklicken (1T, 1M, 3M, 6M, 1J, Max)
- [ ] Position löschen

**Diagramme**
- [ ] Vermögensverlauf: alle sechs Zeiträume, Werte wie oben
- [ ] Sankey: Monat zurückblättern bis vor September 2025 (leer, Hinweistext) und vorwärts bis in die Zukunft

## Yuh-Import (Einstellungen → Yuh-Import)

`yuh_beispiel.csv` ist ein **erfundener** Beispielexport im Yuh-Format. Auf ein Testkonto importieren. Erwartet: **4 Buchung(en) importiert (davon 1 Depot-Kauf)**, Hinweise zu Bonus (kein CHF-Betrag), EUR-Zeile und ungültigem Datum (31/02). Zweiter Import derselben Datei: 0 neue, 4 bereits vorhanden. Es entsteht das Konto „Yuh Anlagen" und eine Depot-Position `TEST.SW`/`TEST`.
