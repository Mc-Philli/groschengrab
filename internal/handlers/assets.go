package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"household-app/internal/models"
)

// AssetType beschreibt eine Art von sonstigem Asset. Schulden (Hypothek,
// Kredit) werden positiv erfasst und vom Vermögen abgezogen.
type AssetType struct {
	Key   string
	Label string
	Debt  bool
}

var assetTypes = []AssetType{
	{Key: "property", Label: "Immobilie"},
	{Key: "vehicle", Label: "Fahrzeug"},
	{Key: "pension", Label: "Vorsorge (Säule 3a, Pensionskasse)"},
	{Key: "cash", Label: "Bargeld"},
	{Key: "valuables", Label: "Wertgegenstand"},
	{Key: "other", Label: "Sonstiges"},
	{Key: "debt", Label: "Schuld (Hypothek, Kredit)", Debt: true},
}

func findAssetType(key string) (AssetType, bool) {
	for _, t := range assetTypes {
		if t.Key == key {
			return t, true
		}
	}
	return AssetType{}, false
}

// AssetTypeLabel ist als Template-Funktion registriert.
func AssetTypeLabel(key string) string {
	if t, ok := findAssetType(key); ok {
		return t.Label
	}
	return key
}

// signedAssetValue liefert den Beitrag eines Assets zum Vermögen.
func signedAssetValue(assetType string, value float64) float64 {
	if t, ok := findAssetType(assetType); ok && t.Debt {
		return -value
	}
	return value
}

// Assets zeigt die Seite "Sonstige Assets".
func (h *Handlers) Assets(w http.ResponseWriter, r *http.Request) {
	assets, err := h.loadOtherAssets()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var total, positive, debts float64
	for _, a := range assets {
		v := signedAssetValue(a.Type, a.CurrentValue)
		total += v
		if v < 0 {
			debts -= v
		} else {
			positive += v
		}
	}

	data := struct {
		Assets          []models.OtherAsset
		AssetTypes      []AssetType
		Total           float64
		PositiveTotal   float64
		DebtTotal       float64
		ErrorMsg        string
		SuccessMsg      string
		Page            string
		Today           string
		CurrentUserName string
	}{
		Assets:          assets,
		AssetTypes:      assetTypes,
		Total:           total,
		PositiveTotal:   positive,
		DebtTotal:       debts,
		ErrorMsg:        r.URL.Query().Get("error"),
		SuccessMsg:      r.URL.Query().Get("success"),
		Page:            "assets",
		Today:           time.Now().Format("2006-01-02"),
		CurrentUserName: currentUser(r).Name,
	}

	if err := h.tmpl.ExecuteTemplate(w, "assets.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handlers) loadOtherAssets() ([]models.OtherAsset, error) {
	rows, err := h.db.Query(`SELECT id, name, type, current_value, substr(updated_at, 1, 10)
		FROM other_assets ORDER BY type = 'debt', name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []models.OtherAsset
	for rows.Next() {
		var a models.OtherAsset
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.CurrentValue, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.IsDebt = signedAssetValue(a.Type, 1) < 0
		list = append(list, a)
	}
	return list, rows.Err()
}

// otherAssetsTotal liefert den aktuellen Nettowert aller sonstigen Assets
// (Schulden abgezogen).
func (h *Handlers) otherAssetsTotal() (float64, error) {
	assets, err := h.loadOtherAssets()
	if err != nil {
		return 0, err
	}
	var total float64
	for _, a := range assets {
		total += signedAssetValue(a.Type, a.CurrentValue)
	}
	return total, nil
}

// parseAssetValueForm liest Wert und Stichtag aus dem Formular.
func parseAssetValueForm(r *http.Request) (float64, string, string) {
	value, err := parseAmount(r.FormValue("value"))
	if err != nil || value < 0 || strings.TrimSpace(r.FormValue("value")) == "" {
		return 0, "", "Bitte einen gültigen Wert (0 oder größer) angeben."
	}
	date := strings.TrimSpace(r.FormValue("valued_at"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, "", "Ungültiges Datum."
	}
	if d.After(time.Now()) {
		return 0, "", "Der Stichtag darf nicht in der Zukunft liegen."
	}
	return value, date, ""
}

// CreateAsset legt ein neues Asset mit Startbewertung an.
func (h *Handlers) CreateAsset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		redirectWithMessage(w, r, "/assets", "error", "Bezeichnung darf nicht leer sein.")
		return
	}
	assetType := r.FormValue("type")
	if _, ok := findAssetType(assetType); !ok {
		redirectWithMessage(w, r, "/assets", "error", "Bitte eine gültige Art auswählen.")
		return
	}
	value, date, msg := parseAssetValueForm(r)
	if msg != "" {
		redirectWithMessage(w, r, "/assets", "error", msg)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO other_assets (name, type, current_value, updated_at) VALUES (?, ?, ?, ?)`,
		name, assetType, value, date)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO asset_valuations (asset_id, valued_at, value) VALUES (?, ?, ?)`,
		id, date, value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/assets", "success", "Asset «"+name+"» hinzugefügt.")
}

// UpdateAssetValue erfasst eine neue Bewertung für ein bestehendes Asset.
func (h *Handlers) UpdateAssetValue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, date, msg := parseAssetValueForm(r)
	if msg != "" {
		redirectWithMessage(w, r, "/assets", "error", msg)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM other_assets WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		redirectWithMessage(w, r, "/assets", "error", "Asset nicht gefunden.")
		return
	}

	// Gibt es für den Stichtag schon eine Bewertung, wird sie ersetzt.
	if _, err := tx.Exec(`DELETE FROM asset_valuations WHERE asset_id = ? AND valued_at = ?`, id, date); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`INSERT INTO asset_valuations (asset_id, valued_at, value) VALUES (?, ?, ?)`,
		id, date, value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Aktueller Wert = jüngste Bewertung (eine nachgetragene ältere
	// Bewertung ändert den aktuellen Wert also nicht).
	if _, err := tx.Exec(`UPDATE other_assets SET
			current_value = (SELECT value FROM asset_valuations WHERE asset_id = ?1 ORDER BY valued_at DESC, id DESC LIMIT 1),
			updated_at    = (SELECT valued_at FROM asset_valuations WHERE asset_id = ?1 ORDER BY valued_at DESC, id DESC LIMIT 1)
		WHERE id = ?1`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/assets", "success", "Wert aktualisiert.")
}

// DeleteAsset entfernt ein Asset samt Wertverlauf.
func (h *Handlers) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Ungültige ID", http.StatusBadRequest)
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM asset_valuations WHERE asset_id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`DELETE FROM other_assets WHERE id = ?`, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	redirectWithMessage(w, r, "/assets", "success", "Asset gelöscht.")
}

type assetValuation struct {
	date  string
	value float64 // bereits mit Vorzeichen (Schulden negativ)
}

// assetValuesByDate liefert für jeden der (aufsteigend sortierten) Tage den
// Nettowert aller sonstigen Assets: je Asset zählt die jüngste Bewertung
// am oder vor dem Tag. Vor der ersten Bewertung zählt ein Asset mit 0.
func (h *Handlers) assetValuesByDate(dates []string) ([]float64, error) {
	rows, err := h.db.Query(`SELECT v.asset_id, a.type, v.valued_at, v.value
		FROM asset_valuations v JOIN other_assets a ON a.id = v.asset_id
		ORDER BY v.asset_id, v.valued_at, v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byAsset := map[int64][]assetValuation{}
	for rows.Next() {
		var id int64
		var typ, date string
		var value float64
		if err := rows.Scan(&id, &typ, &date, &value); err != nil {
			return nil, err
		}
		byAsset[id] = append(byAsset[id], assetValuation{date: date, value: signedAssetValue(typ, value)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]float64, len(dates))
	for _, vals := range byAsset {
		for i, d := range dates {
			// Index der ersten Bewertung, die nach d liegt.
			k := sort.Search(len(vals), func(j int) bool { return vals[j].date > d })
			if k > 0 {
				result[i] += vals[k-1].value
			}
		}
	}
	return result, nil
}
