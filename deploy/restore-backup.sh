#!/usr/bin/env bash
# Stellt die Datenbank aus einem Backup wieder her.
#
#   /opt/household-app/restore-backup.sh                 # listet Backups auf
#   /opt/household-app/restore-backup.sh <backup.db.gz>  # stellt es wieder her
#
# Die aktuelle Datenbank wird vorher als app.db.vor-restore-<Zeit> gesichert.
set -euo pipefail

DB="${APP_DB_PATH:-/opt/household-app/data/app.db}"
DIR="${BACKUP_DIR:-/opt/household-app/backups}"

if [ $# -eq 0 ]; then
    echo "Vorhandene Backups (neueste zuletzt):"
    ls -1tr "$DIR"/app-*.db.gz 2>/dev/null || echo "  (keine gefunden in $DIR)"
    echo
    echo "Wiederherstellen mit: $0 <Datei>"
    exit 0
fi

BACKUP="$1"
[ -f "$BACKUP" ] || { echo "Datei nicht gefunden: $BACKUP" >&2; exit 1; }

# Backup zuerst entpacken und prüfen, bevor irgendetwas überschrieben wird.
TMP_DB="$(mktemp "$(dirname "$DB")/.restore.XXXXXX")"
trap 'rm -f "$TMP_DB"' EXIT
gunzip -c "$BACKUP" > "$TMP_DB"
CHECK="$(sqlite3 "$TMP_DB" "PRAGMA integrity_check;")"
if [ "$CHECK" != "ok" ]; then
    echo "Backup ist beschädigt: $CHECK" >&2
    exit 1
fi

echo "Achtung: Die aktuelle Datenbank wird durch"
echo "  $BACKUP"
echo "ersetzt. Alles, was seit diesem Backup erfasst wurde, geht verloren"
echo "(die aktuelle Datenbank wird aber vorher noch gesichert)."
read -r -p "Fortfahren? (ja/nein) " ANSWER
[ "$ANSWER" = "ja" ] || { echo "Abgebrochen."; exit 1; }

sudo systemctl stop household-app

SAFETY="$DB.vor-restore-$(date +%Y-%m-%d_%H%M%S)"
if [ -f "$DB" ]; then
    cp "$DB" "$SAFETY"
    echo "Aktuelle Datenbank gesichert als: $SAFETY"
fi
# Eventuelle WAL-/Journal-Dateien der alten Datenbank würden sonst auf die
# wiederhergestellte angewendet.
rm -f "$DB-wal" "$DB-shm" "$DB-journal"
mv "$TMP_DB" "$DB"

sudo systemctl start household-app
echo "Wiederhergestellt. Die App läuft wieder."
