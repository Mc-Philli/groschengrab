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
deploy/           systemd-Unit + Einrichtungsskript für den Pi
.github/workflows/deploy.yml   CI/CD-Pipeline
```

## Aktueller Stand & nächste Schritte

Aktuell enthält das Projekt: Datenbankschema (Nutzer, Konten, Buchungen,
Kassenzettel, Wertpapier-Depot, sonstige Assets) und eine erste Dashboard-Seite
mit Konten-Übersicht. Als Nächstes sinnvoll, in dieser Reihenfolge:

1. Login/Mehrbenutzer-Verwaltung
2. Formulare zum manuellen Erfassen von Ein-/Ausgaben
3. Aktien/ETF-Depot mit Kursabfrage (Yahoo-Finance-Endpoint, den auch die
   Python-Bibliothek `yfinance` intern nutzt)
4. Sonstige Assets erfassen
5. Kassenzettel-Upload: Foto → Tesseract (per `os/exec` aufgerufen) →
   einfache Textregeln zur Vorschlagsgenerierung → Bestätigung durch dich
   in der Oberfläche, bevor gebucht wird

Sag Bescheid, mit welchem der Punkte ich weitermachen soll.
