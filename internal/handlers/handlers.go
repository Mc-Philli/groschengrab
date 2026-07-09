package handlers

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
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

// Dashboard zeigt die Startseite: Gesamtvermögen, Konten, Buchungsformular
// und die letzten Buchungen.
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

	categories, err := h.loadCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var totalAssets float64
	for _, a := range accounts {
		totalAssets += a.Balance
	}

	data := struct {
		Accounts        []models.Account
		Transactions    []models.TransactionView
		Categories      []models.Category
		TotalAssets     float64
		ErrorMsg        string
		SuccessMsg      string
		Today           string
		Page            string
		CurrentUserName string
	}{
		Accounts:        accounts,
		Transactions:    transactions,
		Categories:      categories,
		TotalAssets:     totalAssets,
		ErrorMsg:        r.URL.Query().Get("error"),
		SuccessMsg:      r.URL.Query().Get("success"),
		Today:           time.Now().Format("2006-01-02"),
		Page:            "home",
		CurrentUserName: currentUser(r).Name,
	}

	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Settings zeigt die Einstellungsseite: Konto anlegen, Kategorien verwalten,
// Export/Import.
func (h *Handlers) Settings(w http.ResponseWriter, r *http.Request) {
	categories, err := h.loadCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Categories      []models.Category
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		CurrentUserName string
	}{
		Categories:      categories,
		ErrorMsg:        r.URL.Query().Get("error"),
		SuccessMsg:      r.URL.Query().Get("success"),
		Page:            "settings",
		CurrentUserName: currentUser(r).Name,
	}

	if err := h.tmpl.ExecuteTemplate(w, "settings.html", data); err != nil {
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
		SELECT t.id, a.name, COALESCE(b.name, ''), u.name, t.amount, t.booked_at, t.category, t.description, t.kind
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN accounts b ON b.id = t.to_account_id
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
		if err := rows.Scan(&tv.ID, &tv.AccountName, &tv.ToAccountName, &tv.UserName, &tv.Amount, &tv.BookedAt, &tv.Category, &tv.Description, &tv.Kind); err != nil {
			return nil, err
		}
		list = append(list, tv)
	}
	return list, rows.Err()
}

func (h *Handlers) loadCategories() ([]models.Category, error) {
	rows, err := h.db.Query(`SELECT id, name FROM categories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// CreateAccount verarbeitet das Formular "Konto anlegen" (liegt auf der
// Einstellungsseite).
func (h *Handlers) CreateAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	accountType := r.FormValue("type")

	if name == "" {
		redirectWithMessage(w, r, "/settings", "error", "Name darf nicht leer sein.")
		return
	}

	startBalance, err := parseAmount(r.FormValue("start_balance"))
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Startsaldo ist ungültig (Zahl mit Punkt erwartet).")
		return
	}

	if _, err := h.db.Exec(
		`INSERT INTO accounts (name, type, balance) VALUES (?, ?, ?)`,
		name, accountType, startBalance,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/settings", "success", "Konto angelegt.")
}

// transactionRecord bündelt die Rohdaten einer Buchung – wird sowohl vom
// manuellen Formular als auch vom CSV-Import verwendet.
type transactionRecord struct {
	BookedAt    string
	Kind        string // "income", "expense" oder "transfer"
	Amount      float64
	Category    string
	Description string
}

// CreateTransaction verarbeitet das Formular "Buchung erfassen" und
// aktualisiert dabei innerhalb einer Datenbank-Transaktion auch den/die
// betroffenen Kontosaldo/-salden.
func (h *Handlers) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		redirectWithMessage(w, r, "/", "error", "Bitte ein Konto auswählen.")
		return
	}

	kind := r.FormValue("kind")
	if kind != "income" && kind != "expense" && kind != "transfer" {
		redirectWithMessage(w, r, "/", "error", "Ungültige Buchungsart.")
		return
	}

	amount, err := parseAmount(r.FormValue("amount"))
	if err != nil || amount <= 0 {
		redirectWithMessage(w, r, "/", "error", "Bitte einen gültigen Betrag größer 0 angeben.")
		return
	}

	var toAccountID sql.NullInt64
	if kind == "transfer" {
		toID, err := strconv.ParseInt(r.FormValue("to_account_id"), 10, 64)
		if err != nil {
			redirectWithMessage(w, r, "/", "error", "Bitte ein Zielkonto für den Transfer auswählen.")
			return
		}
		if toID == accountID {
			redirectWithMessage(w, r, "/", "error", "Quell- und Zielkonto müssen unterschiedlich sein.")
			return
		}
		toAccountID = sql.NullInt64{Int64: toID, Valid: true}
	}

	bookedAt := strings.TrimSpace(r.FormValue("booked_at"))
	if bookedAt == "" {
		bookedAt = time.Now().Format("2006-01-02")
	}

	user := currentUser(r)

	rec := transactionRecord{
		BookedAt:    bookedAt,
		Kind:        kind,
		Amount:      amount,
		Category:    strings.TrimSpace(r.FormValue("category")),
		Description: strings.TrimSpace(r.FormValue("description")),
	}

	if err := h.insertTransactionAndUpdateBalance(accountID, toAccountID, user.ID, rec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/", "success", "Buchung gespeichert.")
}

// insertTransactionAndUpdateBalance schreibt eine Buchung und passt je nach
// Art (Einnahme/Ausgabe/Transfer) die betroffenen Kontosalden an – alles
// innerhalb einer einzigen Datenbank-Transaktion (alles-oder-nichts).
func (h *Handlers) insertTransactionAndUpdateBalance(accountID int64, toAccountID sql.NullInt64, userID int64, rec transactionRecord) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO transactions (account_id, to_account_id, user_id, amount, booked_at, category, description, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		accountID, toAccountID, userID, rec.Amount, rec.BookedAt, rec.Category, rec.Description, rec.Kind,
	); err != nil {
		return err
	}

	switch rec.Kind {
	case "income":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = ?`, rec.Amount, accountID); err != nil {
			return err
		}
	case "expense":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance - ? WHERE id = ?`, rec.Amount, accountID); err != nil {
			return err
		}
	case "transfer":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance - ? WHERE id = ?`, rec.Amount, accountID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = ?`, rec.Amount, toAccountID.Int64); err != nil {
			return err
		}
	}

	return tx.Commit()
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

// DeleteTransaction löscht eine Buchung und macht ihre Wirkung auf den/die
// Kontosaldo/-salden rückgängig.
func (h *Handlers) DeleteTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Buchungs-ID", http.StatusBadRequest)
		return
	}

	var accountID int64
	var toAccountID sql.NullInt64
	var amount float64
	var kind string
	err = h.db.QueryRow(`SELECT account_id, to_account_id, amount, kind FROM transactions WHERE id = ?`, id).
		Scan(&accountID, &toAccountID, &amount, &kind)
	if err == sql.ErrNoRows {
		redirectWithMessage(w, r, "/", "error", "Diese Buchung wurde nicht gefunden.")
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM transactions WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	switch kind {
	case "expense":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = ?`, amount, accountID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "income":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance - ? WHERE id = ?`, amount, accountID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "transfer":
		if _, err := tx.Exec(`UPDATE accounts SET balance = balance + ? WHERE id = ?`, amount, accountID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if toAccountID.Valid {
			if _, err := tx.Exec(`UPDATE accounts SET balance = balance - ? WHERE id = ?`, amount, toAccountID.Int64); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/", "success", "Buchung gelöscht.")
}

// DeleteAccount löscht ein Konto – aber nur, wenn keine Buchungen mehr daran
// hängen (auch nicht als Transfer-Zielkonto), damit keine verwaisten Daten
// entstehen.
func (h *Handlers) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Konto-ID", http.StatusBadRequest)
		return
	}

	var count int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE account_id = ? OR to_account_id = ?`, id, id).Scan(&count); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if count > 0 {
		redirectWithMessage(w, r, "/", "error", fmt.Sprintf(
			"Konto kann nicht gelöscht werden: %d Buchung(en) hängen noch daran. Bitte zuerst diese Buchungen löschen.", count,
		))
		return
	}

	if _, err := h.db.Exec(`DELETE FROM accounts WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/", "success", "Konto gelöscht.")
}

// CreateCategory verarbeitet das Formular "Kategorie anlegen" auf der
// Einstellungsseite.
func (h *Handlers) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		redirectWithMessage(w, r, "/settings", "error", "Name darf nicht leer sein.")
		return
	}

	if _, err := h.db.Exec(`INSERT INTO categories (name) VALUES (?)`, name); err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Kategorie konnte nicht angelegt werden (gibt es sie schon?).")
		return
	}

	redirectWithMessage(w, r, "/settings", "success", "Kategorie angelegt.")
}

// DeleteCategory entfernt eine Kategorie aus der Auswahlliste. Bereits
// gebuchte Buchungen behalten ihren bisherigen Kategorietext unverändert.
func (h *Handlers) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Kategorie-ID", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`DELETE FROM categories WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/settings", "success", "Kategorie gelöscht.")
}

// redirectWithMessage leitet zu der angegebenen Seite zurück und hängt eine
// Erfolgs- oder Fehlermeldung als URL-Parameter an.
func redirectWithMessage(w http.ResponseWriter, r *http.Request, path, kind, message string) {
	q := url.Values{}
	q.Set(kind, message)
	http.Redirect(w, r, path+"?"+q.Encode(), http.StatusSeeOther)
}

// parseAmount akzeptiert Schweizer Schreibweise: Punkt als Dezimaltrennzeichen,
// Apostroph (optional) als Tausendertrennzeichen, z. B. "1'234.50" oder "12.50".
// Ein Komma wird zur Sicherheit ebenfalls als Dezimaltrennzeichen akzeptiert.
func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") && !strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ",", ".")
	}
	return strconv.ParseFloat(s, 64)
}
