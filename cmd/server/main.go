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

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"chf":   handlers.FormatCHF,
		"txchf": handlers.FormatTransactionAmount,
	}).ParseGlob("web/templates/*.html")
	if err != nil {
		log.Fatalf("Templates konnten nicht geladen werden: %v", err)
	}

	h := handlers.New(database, tmpl)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", h.LoginPage)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("GET /register", h.RegisterPage)
	mux.HandleFunc("POST /register", h.Register)
	mux.HandleFunc("POST /logout", h.Logout)
	mux.HandleFunc("GET /healthz", h.Health)

	mux.HandleFunc("GET /{$}", h.RequireAuth(h.Dashboard))
	mux.HandleFunc("GET /settings", h.RequireAuth(h.Settings))
	mux.HandleFunc("GET /depot", h.RequireAuth(h.Depot))
	mux.HandleFunc("POST /accounts", h.RequireAuth(h.CreateAccount))
	mux.HandleFunc("POST /accounts/{id}/delete", h.RequireAuth(h.DeleteAccount))
	mux.HandleFunc("POST /transactions", h.RequireAuth(h.CreateTransaction))
	mux.HandleFunc("POST /transactions/{id}/delete", h.RequireAuth(h.DeleteTransaction))
	mux.HandleFunc("POST /categories", h.RequireAuth(h.CreateCategory))
	mux.HandleFunc("POST /categories/{id}/delete", h.RequireAuth(h.DeleteCategory))
	mux.HandleFunc("POST /holdings", h.RequireAuth(h.CreateHolding))
	mux.HandleFunc("POST /holdings/{id}/delete", h.RequireAuth(h.DeleteHolding))
	mux.HandleFunc("GET /export/accounts.csv", h.RequireAuth(h.ExportAccounts))
	mux.HandleFunc("GET /export/transactions.csv", h.RequireAuth(h.ExportTransactions))
	mux.HandleFunc("POST /import/accounts", h.RequireAuth(h.ImportAccounts))
	mux.HandleFunc("POST /import/transactions", h.RequireAuth(h.ImportTransactions))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	addr := ":8080"
	log.Printf("Server läuft auf %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
