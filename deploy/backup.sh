#!/usr/bin/env bash
# Erstellt ein Backup der Haushalts-Datenbank.
#
# - Konsistente Kopie per SQLite-Backup-API (funktioniert auch, während die
#   App läuft und schreibt)
# - Integritätsprüfung der Kopie, bevor sie behalten wird
# - gzip-komprimiert, Dateiname mit Zeitstempel
# - Backups älter als BACKUP_KEEP_DAYS Tage werden gelöscht
#
# Läuft nächtlich über den systemd-Timer household-app-backup.timer und vor
# jedem Deploy. Von Hand: /opt/household-app/backup.sh
set -euo pipefail

DB="${APP_DB_PATH:-/opt/household-app/data/app.db}"
DIR="${BACKUP_DIR:-/opt/household-app/backups}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"

if [ ! -f "$DB" ]; then
    echo "Datenbank nicht gefunden: $DB" >&2
    exit 1
fi
command -v sqlite3 >/dev/null || { echo "sqlite3 fehlt (sudo apt-get install sqlite3)" >&2; exit 1; }

mkdir -p "$DIR"

STAMP="$(date +%Y-%m-%d_%H%M%S)"
TARGET="$DIR/app-$STAMP.db.gz"
TMP_DB="$DIR/.app-$STAMP.db.tmp"
TMP_GZ="$TARGET.tmp"
trap 'rm -f "$TMP_DB" "$TMP_GZ"' EXIT

# Bis zu 30 s warten, falls die App gerade eine Schreibsperre hält.
sqlite3 -cmd ".timeout 30000" "$DB" ".backup '$TMP_DB'"

CHECK="$(sqlite3 "$TMP_DB" "PRAGMA integrity_check;")"
if [ "$CHECK" != "ok" ]; then
    echo "Integritätsprüfung der Kopie fehlgeschlagen: $CHECK" >&2
    exit 1
fi

gzip -c "$TMP_DB" > "$TMP_GZ"
mv "$TMP_GZ" "$TARGET"

# Alte Backups aufräumen (nur nach erfolgreichem Backup, das neueste bleibt
# also immer erhalten).
find "$DIR" -maxdepth 1 -name 'app-*.db.gz' -mtime +"$KEEP_DAYS" -print -delete \
    | sed 's/^/Gelöscht (älter als '"$KEEP_DAYS"' Tage): /'

echo "Backup erstellt: $TARGET ($(du -h "$TARGET" | cut -f1))"
