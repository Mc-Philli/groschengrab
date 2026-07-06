#!/usr/bin/env bash
set -euo pipefail

# Ermittelt den Ordner, in dem DIESES Skript liegt (deploy/) – unabhängig
# davon, aus welchem Verzeichnis heraus es aufgerufen wird.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "== Haushalts-App: Grundinstallation auf dem Pi =="

sudo apt-get update
sudo apt-get install -y tesseract-ocr tesseract-ocr-deu golang git

sudo mkdir -p /opt/household-app/data
sudo chown -R "$USER":"$USER" /opt/household-app

echo "== Sudoers-Regel, damit der GitHub-Runner den Dienst neu starten darf =="
RUNNER_USER=$(whoami)
echo "$RUNNER_USER ALL=(ALL) NOPASSWD: /bin/systemctl stop household-app, /bin/systemctl start household-app, /bin/systemctl restart household-app" \
  | sudo tee /etc/sudoers.d/household-app-deploy

echo "== systemd-Dienst installieren =="
sudo cp "$SCRIPT_DIR/household-app.service" /etc/systemd/system/household-app.service
# Trägt den tatsächlich aufrufenden Benutzer ein (nicht jeder Pi-Nutzer heißt "pi")
sudo sed -i "s/^User=.*/User=$RUNNER_USER/" /etc/systemd/system/household-app.service
sudo systemctl daemon-reload
sudo systemctl enable household-app

echo "Fertig. Nächster Schritt: GitHub-Actions-Runner registrieren (siehe README.md)."
