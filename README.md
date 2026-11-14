# Haushalts-App

Eine selbst gehostete Haushalts-App (Konten, Ausgaben/Einnahmen, Aktien/ETFs,
sonstige Assets, Kassenzettel-Scan) für den Betrieb auf einem Raspberry Pi.
Geschrieben in Go, Datenbank SQLite, Oberfläche über serverseitig gerenderte
HTML-Templates mit htmx.

## Wichtiger Architektur-Hinweis: Auto-Deploy ohne offene Ports

Du wolltest volle Automatisierung: Push → Build → läuft auf dem Pi. Das
Problem dabei: GitHub Actions läuft normalerweise in der Cloud und kann einen
Pi hinter deinem Heimrouter nicht direkt erreichen, ohne dass du einen Port
nach außen öffnest (Sicherheitsrisiko).

**Lösung:** Wir installieren einen sogenannten *selbst-gehosteten Runner*
direkt auf dem Pi. Der Runner baut eine ausgehende Verbindung zu GitHub auf
(wie ein normaler Browser-Tab) und fragt dort nach Arbeit. Es muss also
**kein Port** an deinem Router geöffnet werden. Der Build läuft dann direkt
auf dem Pi (kein Cross-Compiling nötig), und im letzten Schritt startet der
Runner den Dienst neu.

## Schritt 1: Pi vorbereiten

```bash
git clone <dein-repo-url>
cd household-app
chmod +x deploy/setup-pi.sh
./deploy/setup-pi.sh
```

Das Skript installiert Go, Tesseract-OCR (inkl. deutschem Sprachpaket), legt
`/opt/household-app` an und richtet den systemd-Dienst ein (noch gestoppt,
da das Binary erst beim ersten Deploy erscheint).

## Schritt 2: GitHub-Actions-Runner auf dem Pi registrieren

1. Im GitHub-Repo: **Settings → Actions → Runners → New self-hosted runner**
2. Betriebssystem **Linux**, Architektur **ARM64** (Pi 4/5 mit 64-Bit-OS)
   oder **ARM** (32-Bit) auswählen
3. Die dort angezeigten Befehle direkt auf dem Pi ausführen (Download,
   `./config.sh` mit dem angezeigten Token, Label z. B. `raspberrypi`)
4. Als Dienst installieren, damit er beim Neustart automatisch läuft:
   ```bash
   sudo ./svc.sh install
   sudo ./svc.sh start
   ```

Danach siehst du den Runner in GitHub als "Idle" markiert.

## Schritt 3: Erster Push

Sobald du auf `main` pushst, läuft automatisch `.github/workflows/deploy.yml`:
Tests → Build → Dienst stoppen → neues Binary einspielen → Dienst starten →
Health-Check. Die App ist danach unter `http://<pi-ip>:8080` erreichbar.

## Backups

Die Datenbank wird **jede Nacht um ca. 3 Uhr** gesichert und zusätzlich
**vor jedem Deploy** (bevor neue Migrationen laufen). Die Backups liegen
gzip-komprimiert unter `/opt/household-app/backups/` und werden nach 30 Tagen
gelöscht. Jede Kopie wird vor dem Speichern auf Integrität geprüft.

**Einmalig einrichten** (auf einem Pi, der schon läuft; bei einer
Neuinstallation erledigt das `setup-pi.sh`):

```bash
cd ~/<repo-ordner> && git pull
./deploy/setup-backup.sh
```

**Nützliche Befehle:**

```bash
/opt/household-app/backup.sh                      # Backup sofort von Hand
ls -lh /opt/household-app/backups                 # vorhandene Backups
systemctl list-timers household-app-backup.timer  # wann läuft das nächste?
journalctl -u household-app-backup -n 20          # Log der letzten Läufe
```

**Wiederherstellen:**

```bash
/opt/household-app/restore-backup.sh                                   # Backups auflisten
/opt/household-app/restore-backup.sh /opt/household-app/backups/app-2026-11-13_030512.db.gz
```

Das Skript prüft das Backup, fragt nach, sichert die aktuelle Datenbank als
`app.db.vor-restore-<Zeit>`, stoppt die App, spielt das Backup ein und startet
sie wieder.

**Wichtig:** Die Backups liegen auf derselben SD-Karte wie die Datenbank. Sie
schützen vor Bedienfehlern, missglückten Updates und einer beschädigten
Datenbank, **nicht** vor einem Ausfall der SD-Karte. Dafür den Speicherort per
`BACKUP_DIR` in `/etc/systemd/system/household-app-backup.service` z. B. auf
einen USB-Stick legen, oder später zusätzlich in die Cloud kopieren.

## Lokale Entwicklung (auf deinem PC)

```bash
make tidy    # Abhängigkeiten laden (einmalig, braucht Internet)
make run     # Server lokal starten auf :8080
make test    # Tests und Codeprüfung
```

## Projektstruktur

```
cmd/server/       Einstiegspunkt (main.go)
internal/db/      Datenbankverbindung + Migrationen
internal/models/  Datenstrukturen
internal/handlers/HTTP-Handler (Seiten-Logik)
migrations/       SQL-Migrationen, werden beim Start automatisch ausgeführt
web/templates/    HTML-Seiten
web/static/       CSS
deploy/           systemd-Units, Einrichtungs- und Backup-Skripte für den Pi
.github/workflows/deploy.yml   CI/CD-Pipeline
```

## Aktueller Stand & nächste Schritte

Fertig:

- Login/Mehrbenutzer (bcrypt + Sessions)
- Konten, Buchungen, Umbuchungen, Kategorien und Kategorie-Regeln
- CSV-Export/-Import
- Aktien/ETF-Depot mit Kursabfrage (Yahoo Finance): mehrere Depots (z. B. Saxo,
  WillBe), Umrechnung in CHF, ISIN-Suche, Abgleich mit der Broker-Anzahl
- Sparpläne: monatlicher CHF-Betrag, aufgeteilt auf ETFs, werden automatisch
  gebucht (auch verpasste Termine), optional mit Kontobelastung
- Sonstige Assets (Immobilie, Fahrzeug, Säule 3a, Bargeld, Schulden, …)
  mit Wertverlauf
- Total Vermögen und Vermögensentwicklung = Konten + Depots + sonstige Assets
- Diagramme: Vermögens- und Depotverlauf, Sankey Einnahmen/Ausgaben
- Nächtliches Datenbank-Backup auf dem Pi (lokal) inkl. Wiederherstellung

Offen:

- Handytaugliches Layout (v. a. breite Tabellen auf der Depot-Seite)
- Buchungen bearbeiten, Konten umbenennen, wiederkehrende Buchungen
- CSV-Import für WIR-Kontoauszüge
- Kassenzettel-Upload: Foto → Tesseract (per `os/exec` aufgerufen) →
  einfache Textregeln zur Vorschlagsgenerierung → Bestätigung durch dich
  in der Oberfläche, bevor gebucht wird
