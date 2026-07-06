package handlers

import (
	"database/sql"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	transactions, err := h.loadTransactions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Accounts     []models.Account
		Transactions []models.TransactionView
	}{Accounts: accounts, Transactions: transactions}

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

func (h *Handlers) loadTransactions() ([]models.TransactionView, error) {
	rows, err := h.db.Query(`
		SELECT t.id, a.name, u.name, t.amount, t.booked_at, t.category, t.description, t.kind
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		JOIN users u ON u.id = t.user_id
		ORDER BY t.booked_at DESC, t.id DESC
		LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.TransactionView
	for rows.Next() {
		var tv models.TransactionView
		if err := rows.Scan(&tv.ID, &tv.AccountName, &tv.UserName, &tv.Amount, &tv.BookedAt, &tv.Category, &tv.Description, &tv.Kind); err != nil {
			return nil, err
		}
		list = append(list, tv)
	}
	return list, rows.Err()
}

// CreateAccount verarbeitet das Formular "Konto anlegen".
func (h *Handlers) CreateAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	accountType := r.FormValue("type")

	if name == "" {
		http.Error(w, "Name darf nicht leer sein", http.StatusBadRequest)
		return
	}

	startBalance, err := parseAmount(r.FormValue("start_balance"))
	if err != nil {
		http.Error(w, "Startsaldo ist ungültig (Zahl mit Komma oder Punkt erwartet)", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(
		`INSERT INTO accounts (name, type, balance) VALUES (?, ?, ?)`,
		name, accountType, startBalance,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// CreateTransaction verarbeitet das Formular "Buchung erfassen" und
// aktualisiert dabei innerhalb einer Transaktion auch den Kontosaldo.
func (h *Handlers) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		http.Error(w, "Bitte ein Konto auswählen", http.StatusBadRequest)
		return
	}

	kind := r.FormValue("kind")
	if kind != "income" && kind != "expense" {
		http.Error(w, "Ungültige Buchungsart", http.StatusBadRequest)
		return
	}

	amount, err := parseAmount(r.FormValue("amount"))
	if err != nil || amount <= 0 {
		http.Error(w, "Bitte einen gültigen Betrag größer 0 angeben", http.StatusBadRequest)
		return
	}

	bookedAt := strings.TrimSpace(r.FormValue("booked_at"))
	if bookedAt == "" {
		bookedAt = time.Now().Format("2006-01-02")
	}

	category := strings.TrimSpace(r.FormValue("category"))
	description := strings.TrimSpace(r.FormValue("description"))

	userID, err := h.findOrCreateUser(r.FormValue("booked_by"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO transactions (account_id, user_id, amount, booked_at, category, description, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		accountID, userID, amount, bookedAt, category, description, kind,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	delta := amount
	if kind == "expense" {
		delta = -amount
	}
	if _, err := tx.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = ?`, delta, accountID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// findOrCreateUser sucht einen Nutzer per Name oder legt ihn ohne Passwort an.
// Übergangslösung, bis das echte Login (nächster Baustein) steht.
func (h *Handlers) findOrCreateUser(name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Unbekannt"
	}

	var id int64
	err := h.db.QueryRow(`SELECT id FROM users WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		res, err := h.db.Exec(
			`INSERT INTO users (name, password_hash, role) VALUES (?, '', 'member')`, name,
		)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	} else if err != nil {
		return 0, err
	}
	return id, nil
}

// parseAmount akzeptiert deutsche ("12,50") und englische ("12.50") Schreibweise.
func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	s = strings.ReplaceAll(s, ",", ".")
	return strconv.ParseFloat(s, 64)
}
