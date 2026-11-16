-- Daueraufträge: wiederkehrende Buchungen (Miete, Lohn, Krankenkasse, Abos).
-- Der Ausführungstag ist der Tag von start_date; in kürzeren Monaten wird
-- auf den Monatsletzten verschoben (z. B. 31. -> 30.4.).
CREATE TABLE recurring_transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    to_account_id INTEGER REFERENCES accounts(id),
    user_id INTEGER NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL CHECK (kind IN ('income', 'expense', 'transfer')),
    amount REAL NOT NULL CHECK (amount > 0),
    category TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    every_months INTEGER NOT NULL DEFAULT 1 CHECK (every_months IN (1, 3, 6, 12)),
    start_date TEXT NOT NULL,
    end_date TEXT,
    active INTEGER NOT NULL DEFAULT 1,
    last_executed_on TEXT,
    last_error TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

ALTER TABLE transactions ADD COLUMN recurring_id INTEGER REFERENCES recurring_transactions(id);
