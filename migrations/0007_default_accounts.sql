-- Standard-Konten für eine neue Installation. Sie werden nur angelegt, wenn
-- noch gar keine Konten existieren – eine bestehende Datenbank bleibt also
-- unverändert. Die Nutzer können weitere Konten anlegen und nicht benötigte
-- Standard-Konten löschen (solange keine Buchungen daran hängen).
INSERT INTO accounts (name, type, balance)
SELECT name, type, 0 FROM (
    SELECT 1 AS pos, 'Privatkonto' AS name, 'Girokonto' AS type
    UNION ALL SELECT 2, 'Sparkonto', 'Sparkonto'
    UNION ALL SELECT 3, 'Bargeld', 'Bar'
)
WHERE NOT EXISTS (SELECT 1 FROM accounts)
ORDER BY pos;
