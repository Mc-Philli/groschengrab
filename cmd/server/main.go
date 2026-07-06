package main

import (
	"html/template"
	"log"
	"net/http"
	"os"

	"household-app/internal/db"
	"household-app/internal/handlers"
)

func main() {
	dbPath := os.Getenv("APP_DB_PATH")
	if dbPath == "" {
		dbPath = "app.db"
	}

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("Datenbank konnte nicht geöffnet werden: %v", err)
	}
	defer database.Close()

	if err := db.RunMigrations(database, "migrations"); err != nil {
		log.Fatalf("Migrationen fehlgeschlagen: %v", err)
	}

	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		log.Fatalf("Templates konnten nicht geladen werden: %v", err)
	}

	h := handlers.New(database, tmpl)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.Dashboard)
	mux.HandleFunc("GET /healthz", h.Health)
	mux.HandleFunc("POST /accounts", h.CreateAccount)
	mux.HandleFunc("POST /transactions", h.CreateTransaction)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	addr := ":8080"
	log.Printf("Server läuft auf %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
