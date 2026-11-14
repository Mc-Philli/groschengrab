#!/usr/bin/env bash
# Richtet das nächtliche Datenbank-Backup auf dem Pi ein. Einmalig ausführen
# (wird auch von setup-pi.sh aufgerufen); erneutes Ausführen schadet nicht.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_USER=$(whoami)

echo "== Backup: sqlite3 installieren =="
sudo apt-get install -y sqlite3

echo "== Backup: Skripte nach /opt/household-app kopieren =="
mkdir -p /opt/household-app/backups
cp "$SCRIPT_DIR/backup.sh" "$SCRIPT_DIR/restore-backup.sh" /opt/household-app/
chmod +x /opt/household-app/backup.sh /opt/household-app/restore-backup.sh

echo "== Backup: systemd-Timer installieren =="
sudo cp "$SCRIPT_DIR/household-app-backup.service" /etc/systemd/system/
sudo cp "$SCRIPT_DIR/household-app-backup.timer" /etc/systemd/system/
sudo sed -i "s/^User=.*/User=$APP_USER/" /etc/systemd/system/household-app-backup.service
sudo systemctl daemon-reload
sudo systemctl enable --now household-app-backup.timer

if [ -f /opt/household-app/data/app.db ]; then
    echo "== Backup: erstes Backup erstellen =="
    sudo systemctl start household-app-backup.service
    ls -lh /opt/household-app/backups
else
    echo "Noch keine Datenbank vorhanden – das erste Backup läuft heute Nacht."
fi

echo "Fertig. Nächstes Backup: $(systemctl list-timers household-app-backup.timer --no-legend | awk '{print $1, $2, $3}')"
