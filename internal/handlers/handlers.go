package handlers

import (
	"database/sql"
	"html/template"
	"net/http"

	"household-app/internal/models"
)

type Handlers struct {
	db   *sql.DB
	tmpl *template.Template
}

func New(db *sql.DB, tmpl *template.Template) *Handlers {
	return &Handlers{db: db, tmpl: tmpl}
}

// Health wird von der Deploy-Pipeline nach jedem Neustart abgefragt.
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (h *Handlers) Dashboard(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Accounts []models.Account
	}{Accounts: accounts}

	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handlers) loadAccounts() ([]models.Account, error) {
	rows, err := h.db.Query(`SELECT id, name, type, balance FROM accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []models.Account
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Balance); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}
