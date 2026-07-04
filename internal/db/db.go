package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	_ "modernc.org/sqlite"
)

// Open öffnet (oder erstellt) die SQLite-Datenbankdatei.
func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := database.Ping(); err != nil {
		return nil, err
	}
	// SQLite verträgt nur eine schreibende Verbindung gleichzeitig gut.
	database.SetMaxOpenConns(1)
	return database, nil
}

// RunMigrations führt alle .sql-Dateien im migrations-Ordner in alphabetischer
// Reihenfolge genau einmal aus. Einfache, aber für den Start ausreichende Lösung.
func RunMigrations(database *sql.DB, dir string) error {
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY
	)`); err != nil {
		return err
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, file := range files {
		name := filepath.Base(file)

		var already int
		err := database.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, name).Scan(&already)
		if err != nil {
			return err
		}
		if already > 0 {
			continue
		}

		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		if _, err := database.Exec(string(content)); err != nil {
			return fmt.Errorf("migration %s fehlgeschlagen: %w", name, err)
		}

		if _, err := database.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			return err
		}

		fmt.Printf("Migration angewendet: %s\n", name)
	}

	return nil
}
