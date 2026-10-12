package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"household-app/internal/models"
)

// loadRules liefert alle Kategorie-Regeln, längste Stichworte zuerst. So
// gewinnt bei mehreren Treffern die spezifischere Regel.
func (h *Handlers) loadRules() ([]models.CategoryRule, error) {
	rows, err := h.db.Query(`SELECT id, keyword, category FROM category_rules ORDER BY LENGTH(keyword) DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.CategoryRule
	for rows.Next() {
		var r models.CategoryRule
		if err := rows.Scan(&r.ID, &r.Keyword, &r.Category); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// matchRule gibt die Kategorie der ersten passenden Regel zurück (leer, wenn
// keine passt). Die Regeln müssen nach Stichwortlänge absteigend sortiert sein
// (siehe loadRules). Groß-/Kleinschreibung spielt keine Rolle.
func matchRule(rules []models.CategoryRule, text string) string {
	lower := strings.ToLower(text)
	for _, r := range rules {
		kw := strings.ToLower(strings.TrimSpace(r.Keyword))
		if kw != "" && strings.Contains(lower, kw) {
			return r.Category
		}
	}
	return ""
}

// CreateRule legt eine Regel an, nimmt die Kategorie in die Auswahlliste auf
// und ordnet bestehende, noch unkategorisierte Buchungen mit passender
// Beschreibung sofort zu.
func (h *Handlers) CreateRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	keyword := strings.TrimSpace(r.FormValue("keyword"))
	category := strings.TrimSpace(r.FormValue("category"))
	if keyword == "" || category == "" {
		redirectWithMessage(w, r, "/settings", "error", "Stichwort und Kategorie dürfen nicht leer sein.")
		return
	}

	if _, err := h.db.Exec(`INSERT INTO category_rules (keyword, category) VALUES (?, ?)`, keyword, category); err != nil {
		redirectWithMessage(w, r, "/settings", "error", "Regel konnte nicht angelegt werden (gibt es das Stichwort schon?).")
		return
	}

	if _, err := h.db.Exec(`INSERT OR IGNORE INTO categories (name) VALUES (?)`, category); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res, err := h.db.Exec(
		`UPDATE transactions SET category = ?
		 WHERE (category IS NULL OR TRIM(category) = '')
		   AND kind != 'transfer'
		   AND instr(lower(COALESCE(description, '')), lower(?)) > 0`,
		category, keyword,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, _ := res.RowsAffected()

	msg := "Regel angelegt."
	if updated > 0 {
		msg = fmt.Sprintf("Regel angelegt, %d bestehende Buchung(en) zugeordnet.", updated)
	}
	redirectWithMessage(w, r, "/settings", "success", msg)
}

// DeleteRule entfernt eine Regel. Bereits zugeordnete Buchungen bleiben
// unverändert.
func (h *Handlers) DeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige Regel-ID", http.StatusBadRequest)
		return
	}

	if _, err := h.db.Exec(`DELETE FROM category_rules WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/settings", "success", "Regel gelöscht.")
}
