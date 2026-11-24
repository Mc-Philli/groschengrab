package handlers

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backupStatus ist das Ergebnis des letzten Backups, wie es deploy/backup.sh
// nach $BACKUP_DIR/status schreibt.
type backupStatus struct {
	Found  bool
	When   string // TT.MM.JJJJ HH:MM
	Stale  bool   // älter als 36 Stunden: der nächtliche Lauf hat nicht geklappt
	File   string
	Size   string
	Copy   string // Ergebnis der Kopien weg von der SD-Karte, leer = keine eingerichtet
	CopyOK bool
}

func backupDir() string {
	if d := os.Getenv("BACKUP_DIR"); d != "" {
		return d
	}
	return "/opt/household-app/backups"
}

func readBackupStatus(dir string, now time.Time) backupStatus {
	f, err := os.Open(filepath.Join(dir, "status"))
	if err != nil {
		return backupStatus{}
	}
	defer f.Close()

	st := backupStatus{Found: true}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "time":
			if t, err := time.ParseInLocation("2006-01-02T15:04:05", val, now.Location()); err == nil {
				st.When = t.Format("02.01.2006 15:04")
				st.Stale = now.Sub(t) > 36*time.Hour
			}
		case "file":
			st.File = val
		case "size":
			st.Size = val
		case "copy":
			st.Copy = val
		}
	}
	if st.Copy != "" {
		st.CopyOK = true
		for _, part := range strings.Split(st.Copy, ";") {
			if !strings.HasSuffix(strings.TrimSpace(part), ": ok") {
				st.CopyOK = false
			}
		}
	}
	return st
}
