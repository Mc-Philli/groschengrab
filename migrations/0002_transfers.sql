-- SQLite erlaubt kein nachträgliches Ändern von CHECK-Constraints, daher wird
-- die Tabelle neu angelegt, die Daten kopiert und die alte Tabelle ersetzt.

CREATE TABLE transactions_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    to_account_id INTEGER REFERENCES accounts(id),
    user_id INTEGER NOT NULL REFERENCES users(id),
    amount REAL NOT NULL,
    booked_at TEXT NOT NULL,
    category TEXT,
    description TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('income', 'expense', 'transfer')),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO transactions_new (id, account_id, to_account_id, user_id, amount, booked_at, category, description, kind, created_at)
SELECT id, account_id, NULL, user_id, amount, booked_at, category, description, kind, created_at
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;
