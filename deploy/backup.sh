#!/usr/bin/env bash
# Erstellt ein Backup der Haushalts-Datenbank.
#
# - Konsistente Kopie per SQLite-Backup-API (funktioniert auch, während die
#   App läuft und schreibt)
# - Integritätsprüfung der Kopie, bevor sie behalten wird
# - gzip-komprimiert, Dateiname mit Zeitstempel
# - Backups älter als BACKUP_KEEP_DAYS Tage werden gelöscht
#
# - optional zusätzlich eine Kopie weg von der SD-Karte: auf einen USB-Stick
#   (BACKUP_COPY_DIR) und/oder per rclone in die Cloud (BACKUP_RCLONE_REMOTE),
#   eingestellt in /opt/household-app/backup.conf
# - schreibt das Ergebnis nach $BACKUP_DIR/status, die App zeigt es in den
#   Einstellungen an
#
# Läuft nächtlich über den systemd-Timer household-app-backup.timer und vor
# jedem Deploy. Von Hand: /opt/household-app/backup.sh
set -euo pipefail

CONF="${BACKUP_CONF:-/opt/household-app/backup.conf}"
# shellcheck source=/dev/null
[ -f "$CONF" ] && . "$CONF"

DB="${APP_DB_PATH:-/opt/household-app/data/app.db}"
DIR="${BACKUP_DIR:-/opt/household-app/backups}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-30}"
COPY_DIR="${BACKUP_COPY_DIR:-}"
RCLONE_REMOTE="${BACKUP_RCLONE_REMOTE:-}"
COPY_KEEP_DAYS="${BACKUP_COPY_KEEP_DAYS:-90}"
# Markierungsdatei im Kopierziel: Fehlt sie, ist der USB-Stick vermutlich
# nicht eingehängt – dann nicht still auf die SD-Karte darunter schreiben.
MARKER=".haushalt-backup"

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

SIZE="$(du -h "$TARGET" | cut -f1)"
echo "Backup erstellt: $TARGET ($SIZE)"

# --- Kopien weg von der SD-Karte -------------------------------------------
# Fehler hier brechen das Backup (und einen Deploy) nicht ab, landen aber im
# Status und damit in der App.
COPY_RESULT=""
note() { COPY_RESULT="${COPY_RESULT:+$COPY_RESULT; }$1"; }

if [ -n "$COPY_DIR" ]; then
    if [ ! -f "$COPY_DIR/$MARKER" ]; then
        note "USB: Ziel $COPY_DIR nicht bereit (Stick eingesteckt? Datei $MARKER fehlt)"
    elif cp "$TARGET" "$COPY_DIR/.$(basename "$TARGET").tmp" \
         && mv "$COPY_DIR/.$(basename "$TARGET").tmp" "$COPY_DIR/$(basename "$TARGET")"; then
        find "$COPY_DIR" -maxdepth 1 -name 'app-*.db.gz' -mtime +"$COPY_KEEP_DAYS" -delete || true
        note "USB: ok"
    else
        note "USB: Kopieren nach $COPY_DIR fehlgeschlagen"
    fi
fi

if [ -n "$RCLONE_REMOTE" ]; then
    if ! command -v rclone >/dev/null; then
        note "Cloud: rclone fehlt (sudo apt-get install rclone)"
    elif rclone copy --quiet "$TARGET" "$RCLONE_REMOTE"; then
        rclone delete --quiet --min-age "${COPY_KEEP_DAYS}d" --include 'app-*.db.gz' "$RCLONE_REMOTE" || true
        note "Cloud: ok"
    else
        note "Cloud: Hochladen nach $RCLONE_REMOTE fehlgeschlagen"
    fi
fi

[ -n "$COPY_RESULT" ] && echo "Kopien: $COPY_RESULT"
case "$COPY_RESULT" in *fehl*|*nicht\ bereit*|*fehlt*) echo "WARNUNG: $COPY_RESULT" >&2 ;; esac

# Status für die App (Einstellungen → Backups).
{
    echo "time=$(date +%Y-%m-%dT%H:%M:%S)"
    echo "file=$(basename "$TARGET")"
    echo "size=$SIZE"
    echo "copy=$COPY_RESULT"
} > "$DIR/status.tmp" && mv "$DIR/status.tmp" "$DIR/status"
