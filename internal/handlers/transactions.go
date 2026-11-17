package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"household-app/internal/models"
)

// accountTypes ist die Auswahl beim Anlegen und Bearbeiten von Konten.
var accountTypes = []string{"Girokonto", "Sparkonto", "Bar", "Depot", "Sonstiges"}

// txForm sind die geprüften Felder aus einem Buchungsformular (manuelle
// Buchung, Bearbeiten, Dauerauftrag).
type txForm struct {
	AccountID   int64
	ToAccountID sql.NullInt64
	Record      transactionRecord
}

// parseTxForm liest Konto, Art, Zielkonto, Betrag, Kategorie und
// Beschreibung. Bei einem Fehler ist msg die Meldung für den Nutzer.
func parseTxForm(r *http.Request) (f txForm, msg string) {
	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil {
		return f, "Bitte ein Konto auswählen."
	}
	kind := r.FormValue("kind")
	if kind != "income" && kind != "expense" && kind != "transfer" {
		return f, "Ungültige Buchungsart."
	}
	amount, err := parseAmount(r.FormValue("amount"))
	if err != nil || amount <= 0 {
		return f, "Bitte einen gültigen Betrag größer 0 angeben."
	}
	f.AccountID = accountID
	if kind == "transfer" {
		toID, err := strconv.ParseInt(r.FormValue("to_account_id"), 10, 64)
		if err != nil {
			return f, "Bitte ein Zielkonto für den Transfer auswählen."
		}
		if toID == accountID {
			return f, "Quell- und Zielkonto müssen unterschiedlich sein."
		}
		f.ToAccountID = sql.NullInt64{Int64: toID, Valid: true}
	}
	f.Record = transactionRecord{
		Kind:        kind,
		Amount:      math.Round(amount*100) / 100,
		Category:    strings.TrimSpace(r.FormValue("category")),
		Description: strings.TrimSpace(r.FormValue("description")),
	}
	if kind == "transfer" {
		f.Record.Category = ""
	}
	return f, ""
}

// checkTxAccounts prüft, ob die gewählten Konten existieren.
func (h *Handlers) checkTxAccounts(f txForm) (string, error) {
	ids := []int64{f.AccountID}
	if f.ToAccountID.Valid {
		ids = append(ids, f.ToAccountID.Int64)
	}
	for _, id := range ids {
		var n int
		if err := h.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return "Das gewählte Konto gibt es nicht mehr.", nil
		}
	}
	return "", nil
}

// txFormView füllt das gemeinsame Formular-Template "txfields".
type txFormView struct {
	Accounts    []models.Account
	Categories  []string
	AccountID   int64
	ToAccountID int64
	Kind        string
	Amount      string
	Category    string
	Description string
}

// newTxFormView baut die Formularansicht; die aktuelle Kategorie bleibt
// auswählbar, auch wenn sie nicht (mehr) in der Kategorienliste steht.
func newTxFormView(accounts []models.Account, categories []models.Category, kind string, accountID, toAccountID int64, amount float64, category, description string) txFormView {
	v := txFormView{
		Accounts:    accounts,
		AccountID:   accountID,
		ToAccountID: toAccountID,
		Kind:        kind,
		Category:    category,
		Description: description,
	}
	if v.Kind == "" {
		v.Kind = "expense"
	}
	if amount > 0 {
		v.Amount = strconv.FormatFloat(amount, 'f', 2, 64)
	}
	found := category == ""
	for _, c := range categories {
		v.Categories = append(v.Categories, c.Name)
		if c.Name == category {
			found = true
		}
	}
	if !found {
		v.Categories = append(v.Categories, category)
	}
	return v
}

// EditTransactionPage zeigt das Formular zum Bearbeiten einer Buchung.
func (h *Handlers) EditTransactionPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Buchungs-ID", http.StatusBadRequest)
		return
	}
	var (
		accountID         int64
		toAccountID       sql.NullInt64
		amount            float64
		bookedAt, kind    string
		category, desc    string
		userName, created string
		recurringID       sql.NullInt64
	)
	err = h.db.QueryRow(`SELECT t.account_id, t.to_account_id, t.amount, t.booked_at, t.kind,
			COALESCE(t.category, ''), COALESCE(t.description, ''), u.name, t.created_at, t.recurring_id
		FROM transactions t JOIN users u ON u.id = t.user_id WHERE t.id = ?`, id).
		Scan(&accountID, &toAccountID, &amount, &bookedAt, &kind, &category, &desc, &userName, &created, &recurringID)
	if err == sql.ErrNoRows {
		redirectWithMessage(w, r, "/", "error", "Diese Buchung wurde nicht gefunden.")
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categories, err := h.loadCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		ID              int64
		Form            txFormView
		BookedAt        string
		UserName        string
		CreatedAt       string
		Recurring       bool
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		CurrentUserName string
	}{
		ID:              id,
		Form:            newTxFormView(accounts, categories, kind, accountID, toAccountID.Int64, amount, category, desc),
		BookedAt:        bookedAt,
		UserName:        userName,
		CreatedAt:       created,
		Recurring:       recurringID.Valid,
		ErrorMsg:        r.URL.Query().Get("error"),
		Page:            "home",
		CurrentUserName: currentUser(r).Name,
	}
	if err := h.tmpl.ExecuteTemplate(w, "transaction_edit.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// UpdateTransaction speichert eine geänderte Buchung. Die alte Wirkung auf
// die Kontosalden wird rückgängig gemacht und die neue verbucht – alles in
// einer Datenbank-Transaktion.
func (h *Handlers) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Buchungs-ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/transactions/%d/edit", id)

	f, msg := parseTxForm(r)
	if msg == "" {
		msg, err = h.checkTxAccounts(f)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	bookedAt := strings.TrimSpace(r.FormValue("booked_at"))
	if msg == "" {
		if _, err := time.Parse("2006-01-02", bookedAt); err != nil {
			msg = "Bitte ein gültiges Datum angeben."
		}
	}
	if msg != "" {
		redirectWithMessage(w, r, back, "error", msg)
		return
	}

	if err := h.updateTransaction(id, f, bookedAt); err == sql.ErrNoRows {
		redirectWithMessage(w, r, "/", "error", "Diese Buchung wurde nicht gefunden.")
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectWithMessage(w, r, "/", "success", "Buchung geändert.")
}

func (h *Handlers) updateTransaction(id int64, f txForm, bookedAt string) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var oldAccount int64
	var oldTo sql.NullInt64
	var oldAmount float64
	var oldKind string
	if err := tx.QueryRow(`SELECT account_id, to_account_id, amount, kind FROM transactions WHERE id = ?`, id).
		Scan(&oldAccount, &oldTo, &oldAmount, &oldKind); err != nil {
		return err
	}
	if err := applyBalanceTx(tx, oldKind, oldAmount, oldAccount, oldTo, -1); err != nil {
		return err
	}
	rec := f.Record
	if _, err := tx.Exec(`UPDATE transactions SET account_id = ?, to_account_id = ?, amount = ?, booked_at = ?,
			category = ?, description = ?, kind = ? WHERE id = ?`,
		f.AccountID, f.ToAccountID, rec.Amount, bookedAt, rec.Category, rec.Description, rec.Kind, id); err != nil {
		return err
	}
	if err := applyBalanceTx(tx, rec.Kind, rec.Amount, f.AccountID, f.ToAccountID, 1); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateAccount benennt ein Konto um, ändert den Typ und gleicht auf Wunsch
// den Saldo ab: Weicht der eingegebene Saldo vom App-Saldo ab, wird die
// Differenz als Buchung "Saldo-Abgleich" erfasst (so bleibt der
// Vermögensverlauf stimmig).
func (h *Handlers) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Konto-ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		redirectWithMessage(w, r, "/settings", "error", "Name darf nicht leer sein.")
		return
	}
	accountType := strings.TrimSpace(r.FormValue("type"))
	if accountType == "" {
		accountType = "Sonstiges"
	}
	balanceInput := strings.TrimSpace(r.FormValue("balance"))
	var target float64
	if balanceInput != "" {
		target, err = parseAmount(balanceInput)
		if err != nil {
			redirectWithMessage(w, r, "/settings", "error", "Saldo ist ungültig (Zahl mit Punkt erwartet).")
			return
		}
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var current float64
	if err := tx.QueryRow(`SELECT balance FROM accounts WHERE id = ?`, id).Scan(&current); err == sql.ErrNoRows {
		redirectWithMessage(w, r, "/settings", "error", "Konto nicht gefunden.")
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`UPDATE accounts SET name = ?, type = ? WHERE id = ?`, name, accountType, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	msg := "Konto gespeichert."
	diff := math.Round((target-current)*100) / 100
	if balanceInput != "" && diff != 0 {
		rec := transactionRecord{
			BookedAt:    time.Now().Format("2006-01-02"),
			Kind:        "income",
			Amount:      diff,
			Description: "Saldo-Abgleich",
		}
		if diff < 0 {
			rec.Kind, rec.Amount = "expense", -diff
		}
		if err := insertTransactionTx(tx, id, sql.NullInt64{}, currentUser(r).ID, rec, ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		msg = fmt.Sprintf("Konto gespeichert, Saldo um %s abgeglichen.", FormatTransactionAmount(rec.Kind, rec.Amount))
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectWithMessage(w, r, "/settings", "success", msg)
}

// NewTransactionPage ist das Buchungsformular als eigene Seite, erreichbar
// über den runden +-Knopf auf dem Handy.
func (h *Handlers) NewTransactionPage(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.loadAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	categories, err := h.loadCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Vorauswahl der Art, z. B. aus dem Menü des +-Knopfs (?kind=income).
	kind := r.URL.Query().Get("kind")
	if kind != "income" && kind != "transfer" {
		kind = "expense"
	}
	data := struct {
		Form            txFormView
		HasAccounts     bool
		Today           string
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		CurrentUserName string
	}{
		Form:            newTxFormView(accounts, categories, kind, firstAccountID(accounts), 0, 0, "", ""),
		HasAccounts:     len(accounts) > 0,
		Today:           today(),
		ErrorMsg:        r.URL.Query().Get("error"),
		Page:            "newtx",
		CurrentUserName: currentUser(r).Name,
	}
	if err := h.tmpl.ExecuteTemplate(w, "transaction_new.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
