package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DeleteAccount löscht ein Konto samt allen Buchungen, die daran hängen.
// Umbuchungen von/auf andere Konten werden dort rückgängig gemacht, als
// hätte es sie nie gegeben. Daueraufträge dieses Kontos werden gelöscht,
// Sparpläne belasten danach kein Konto mehr. Bereits gekaufte ETF-Anteile
// bleiben im Depot.
func (h *Handlers) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Konto-ID", http.StatusBadRequest)
		return
	}
	name, n, err := h.deleteAccount(id)
	if err == sql.ErrNoRows {
		redirectWithMessage(w, r, "/settings", "error", "Konto nicht gefunden.")
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msg := fmt.Sprintf("Konto «%s» gelöscht.", name)
	if n > 0 {
		msg = fmt.Sprintf("Konto «%s» und %d Buchung(en) gelöscht.", name, n)
	}
	redirectWithMessage(w, r, "/settings", "success", msg)
}

func (h *Handlers) deleteAccount(id int64) (name string, deleted int, err error) {
	tx, err := h.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()

	if err := tx.QueryRow(`SELECT name FROM accounts WHERE id = ?`, id).Scan(&name); err != nil {
		return "", 0, err
	}

	type txRow struct {
		accountID int64
		to        sql.NullInt64
		amount    float64
		kind      string
	}
	rows, err := tx.Query(`SELECT account_id, to_account_id, amount, kind FROM transactions
		WHERE account_id = ?1 OR to_account_id = ?1`, id)
	if err != nil {
		return "", 0, err
	}
	var list []txRow
	for rows.Next() {
		var t txRow
		if err := rows.Scan(&t.accountID, &t.to, &t.amount, &t.kind); err != nil {
			rows.Close()
			return "", 0, err
		}
		list = append(list, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	// Wirkung auf die anderen Konten rückgängig machen (das gelöschte Konto
	// selbst ist danach egal).
	for _, t := range list {
		if err := applyBalanceTx(tx, t.kind, t.amount, t.accountID, t.to, -1); err != nil {
			return "", 0, err
		}
	}

	for _, q := range []string{
		`DELETE FROM transactions WHERE account_id = ?1 OR to_account_id = ?1`,
		`UPDATE transactions SET recurring_id = NULL WHERE recurring_id IN
			(SELECT id FROM recurring_transactions WHERE account_id = ?1 OR to_account_id = ?1)`,
		`DELETE FROM recurring_transactions WHERE account_id = ?1 OR to_account_id = ?1`,
		`UPDATE savings_plans SET account_id = NULL WHERE account_id = ?1`,
		`DELETE FROM accounts WHERE id = ?1`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return "", 0, err
		}
	}
	return name, len(list), tx.Commit()
}

// resetTables sind alle Tabellen mit Finanzdaten. Nutzer und Logins
// bleiben beim Zurücksetzen erhalten.
var resetTables = []string{
	"receipts", "transactions", "recurring_transactions",
	"savings_plan_items", "savings_plans", "holdings",
	"asset_valuations", "other_assets", "accounts",
}

// ResetData löscht alle Finanzdaten für einen Neustart und legt die
// Standard-Konten wieder an. Vorher wird eine Sicherheitskopie der
// Datenbank neben die Datenbankdatei geschrieben.
func (h *Handlers) ResetData(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.ToUpper(strings.TrimSpace(r.FormValue("confirm"))) != "LÖSCHEN" {
		redirectWithMessage(w, r, "/settings", "error", "Nichts gelöscht: Bitte zur Bestätigung LÖSCHEN eintippen.")
		return
	}
	keepSetup := r.FormValue("keep_setup") != ""

	backup, err := h.safetyCopy("vor-reset")
	if err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Nichts gelöscht: Sicherheitskopie fehlgeschlagen ("+err.Error()+").")
		return
	}
	if err := h.resetData(keepSetup); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	msg := "Alle Daten gelöscht – du kannst frisch anfangen."
	if backup != "" {
		msg += " Sicherheitskopie: " + backup
	}
	redirectWithMessage(w, r, "/settings", "success", msg)
}

func (h *Handlers) resetData(keepSetup bool) error {
	tables := resetTables
	if !keepSetup {
		tables = append(append([]string{}, tables...), "category_rules", "categories")
	}
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range tables {
		if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
			return err
		}
		// IDs wieder bei 1 beginnen lassen.
		if _, err := tx.Exec(`DELETE FROM sqlite_sequence WHERE name = ?`, t); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO accounts (name, type, balance) VALUES
		('Privatkonto', 'Girokonto', 0), ('Sparkonto', 'Sparkonto', 0), ('Bargeld', 'Bar', 0)`); err != nil {
		return err
	}
	return tx.Commit()
}

// safetyCopy schreibt eine vollständige Kopie der Datenbank neben die
// Datenbankdatei (z. B. app.db.vor-reset-20261116-0930) und liefert den Pfad.
func (h *Handlers) safetyCopy(label string) (string, error) {
	var seq int
	var name, file string
	if err := h.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		return "", err
	}
	if file == "" { // In-Memory-Datenbank (Tests)
		return "", nil
	}
	path := fmt.Sprintf("%s.%s-%s", file, label, time.Now().Format("20060102-150405"))
	if _, err := h.db.Exec(`VACUUM INTO ?`, path); err != nil {
		return "", err
	}
	return path, nil
}
