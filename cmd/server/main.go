package main

import (
	"html/template"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"

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

	// Versionsstempel für CSS/JS-Links: ändert sich bei jedem Start (also
	// jedem Deploy), damit Browser keine veraltete Datei aus dem Cache nehmen.
	assetVersion := strconv.FormatInt(time.Now().Unix(), 36)

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"static":    func(name string) string { return "/static/" + name + "?v=" + assetVersion },
		"chf":       handlers.FormatCHF,
		"txchf":     handlers.FormatTransactionAmount,
		"assetType": handlers.AssetTypeLabel,
		"qty":       handlers.FormatQuantity,
		"initial":   handlers.Initial,
		"schedule":  handlers.ScheduleFields,
		"sub":       func(a, b float64) float64 { return a - b },
		"dict":      handlers.Dict,
	}).ParseGlob("web/templates/*.html")
	if err != nil {
		log.Fatalf("Templates konnten nicht geladen werden: %v", err)
	}

	h := handlers.New(database, tmpl)
	// Prüft stündlich, ob Daueraufträge oder Sparpläne fällig sind, und bucht sie.
	h.StartScheduler()

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
	mux.HandleFunc("POST /accounts/{id}", h.RequireAuth(h.UpdateAccount))
	mux.HandleFunc("POST /accounts/{id}/delete", h.RequireAuth(h.DeleteAccount))
	mux.HandleFunc("POST /settings/reset", h.RequireAuth(h.ResetData))
	mux.HandleFunc("POST /transactions", h.RequireAuth(h.CreateTransaction))
	mux.HandleFunc("GET /transactions/new", h.RequireAuth(h.NewTransactionPage))
	mux.HandleFunc("GET /transactions/{id}/edit", h.RequireAuth(h.EditTransactionPage))
	mux.HandleFunc("POST /transactions/{id}", h.RequireAuth(h.UpdateTransaction))
	mux.HandleFunc("POST /transactions/{id}/delete", h.RequireAuth(h.DeleteTransaction))
	mux.HandleFunc("GET /recurring", h.RequireAuth(h.RecurringPage))
	mux.HandleFunc("POST /recurring", h.RequireAuth(h.CreateRecurring))
	mux.HandleFunc("POST /recurring/{id}", h.RequireAuth(h.UpdateRecurring))
	mux.HandleFunc("POST /recurring/{id}/toggle", h.RequireAuth(h.ToggleRecurring))
	mux.HandleFunc("POST /recurring/{id}/delete", h.RequireAuth(h.DeleteRecurring))
	mux.HandleFunc("POST /categories", h.RequireAuth(h.CreateCategory))
	mux.HandleFunc("POST /categories/{id}/delete", h.RequireAuth(h.DeleteCategory))
	mux.HandleFunc("POST /holdings", h.RequireAuth(h.CreateHolding))
	mux.HandleFunc("POST /holdings/{id}/delete", h.RequireAuth(h.DeleteHolding))
	mux.HandleFunc("POST /positions/reconcile", h.RequireAuth(h.ReconcilePosition))
	mux.HandleFunc("POST /positions/move", h.RequireAuth(h.MovePosition))
	mux.HandleFunc("GET /api/ticker-search", h.RequireAuth(h.TickerSearch))
	mux.HandleFunc("POST /depot/refresh-prices", h.RequireAuth(h.RefreshPrices))
	mux.HandleFunc("POST /savings-plans", h.RequireAuth(h.CreateSavingsPlan))
	mux.HandleFunc("POST /savings-plans/run", h.RequireAuth(h.RunSavingsPlansNow))
	mux.HandleFunc("POST /savings-plans/{id}", h.RequireAuth(h.UpdateSavingsPlan))
	mux.HandleFunc("POST /savings-plans/{id}/toggle", h.RequireAuth(h.ToggleSavingsPlan))
	mux.HandleFunc("POST /savings-plans/{id}/delete", h.RequireAuth(h.DeleteSavingsPlan))
	mux.HandleFunc("GET /assets", h.RequireAuth(h.Assets))
	mux.HandleFunc("POST /assets", h.RequireAuth(h.CreateAsset))
	mux.HandleFunc("POST /assets/{id}/value", h.RequireAuth(h.UpdateAssetValue))
	mux.HandleFunc("POST /assets/{id}/delete", h.RequireAuth(h.DeleteAsset))
	mux.HandleFunc("GET /export/accounts.csv", h.RequireAuth(h.ExportAccounts))
	mux.HandleFunc("GET /export/transactions.csv", h.RequireAuth(h.ExportTransactions))
	mux.HandleFunc("POST /import/accounts", h.RequireAuth(h.ImportAccounts))
	mux.HandleFunc("POST /import/transactions", h.RequireAuth(h.ImportTransactions))
	mux.HandleFunc("GET /import", h.RequireAuth(h.ImportPage))
	mux.HandleFunc("POST /import/statement", h.RequireAuth(h.PreviewImport))
	mux.HandleFunc("POST /import/statement/confirm", h.RequireAuth(h.ConfirmImport))
	mux.HandleFunc("POST /rules", h.RequireAuth(h.CreateRule))
	mux.HandleFunc("POST /rules/{id}/delete", h.RequireAuth(h.DeleteRule))
	mux.HandleFunc("GET /api/networth-history", h.RequireAuth(h.NetWorthHistory))
	mux.HandleFunc("GET /api/depot-history", h.RequireAuth(h.DepotHistory))
	mux.HandleFunc("GET /report", h.RequireAuth(h.ReportPage))
	mux.HandleFunc("GET /api/sankey", h.RequireAuth(h.SankeyData))
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Standardmäßig Port 8080. Für eine zweite Test-Instanz neben dem
	// produktiven Dienst kann der Port per APP_ADDR überschrieben werden
	// (zusammen mit APP_DB_PATH für eine eigene Datenbank).
	addr := os.Getenv("APP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("Server läuft auf %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
