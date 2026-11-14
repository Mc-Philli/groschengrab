-- Depot-Paket: Positionen gehören zu einem Depot (z. B. Saxo, WillBe),
-- können einen Anzeigenamen haben und merken sich, wie sie entstanden sind.
--
-- Eine Zeile in holdings ist ein einzelner Kauf ("Tranche"). Die Depot-Seite
-- fasst alle Tranchen desselben Tickers im selben Depot zu einer Position
-- zusammen.
--   source = 'manual'       von Hand erfasst
--   source = 'savings_plan' automatisch durch einen Sparplan
--   source = 'correction'   Abgleich auf die tatsächliche Anzahl beim Broker
ALTER TABLE holdings ADD COLUMN depot TEXT NOT NULL DEFAULT '';
ALTER TABLE holdings ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE holdings ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE holdings ADD COLUMN savings_plan_id INTEGER;

-- Sparpläne: fester CHF-Betrag pro Monat, aufgeteilt auf mehrere ETFs.
CREATE TABLE savings_plans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    depot TEXT NOT NULL,
    amount REAL NOT NULL,                     -- CHF pro Ausführung
    day_of_month INTEGER NOT NULL CHECK (day_of_month BETWEEN 1 AND 28),
    start_date TEXT NOT NULL,                 -- erste mögliche Ausführung
    account_id INTEGER REFERENCES accounts(id), -- optional: Konto, das belastet wird
    user_id INTEGER NOT NULL REFERENCES users(id),
    active INTEGER NOT NULL DEFAULT 1,
    last_executed_on TEXT,                    -- Stichtag der letzten Ausführung
    last_error TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE savings_plan_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id INTEGER NOT NULL REFERENCES savings_plans(id),
    ticker TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    weight REAL NOT NULL CHECK (weight > 0)   -- Anteil in Prozent
);
