-- Kategorie-Regeln: Stichwort (ohne Groß-/Kleinschreibung) -> Kategorie.
-- Werden beim Import angewendet und auf bestehende, unkategorisierte Buchungen.
CREATE TABLE category_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    keyword TEXT NOT NULL UNIQUE COLLATE NOCASE,
    category TEXT NOT NULL
);

-- Fingerabdruck importierter Buchungen, damit ein erneuter Import derselben
-- (oder einer überlappenden) Datei keine Duplikate erzeugt.
ALTER TABLE transactions ADD COLUMN import_hash TEXT;

CREATE UNIQUE INDEX idx_transactions_import_hash
    ON transactions(import_hash)
    WHERE import_hash IS NOT NULL;
