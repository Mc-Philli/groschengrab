-- Wertverlauf der sonstigen Assets (Immobilie, Auto, Säule 3a, ...).
-- Jede Wertanpassung wird mit Stichtag gespeichert, damit die
-- Vermögensentwicklung auch rückblickend stimmt. other_assets.current_value
-- enthält jeweils den Wert der jüngsten Bewertung.
CREATE TABLE asset_valuations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id INTEGER NOT NULL REFERENCES other_assets(id),
    valued_at TEXT NOT NULL,
    value REAL NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_asset_valuations_asset ON asset_valuations(asset_id, valued_at);

-- Bereits vorhandene Assets (falls welche existieren) erhalten eine
-- Startbewertung mit ihrem aktuellen Wert.
INSERT INTO asset_valuations (asset_id, valued_at, value)
SELECT id, substr(updated_at, 1, 10), current_value FROM other_assets;
