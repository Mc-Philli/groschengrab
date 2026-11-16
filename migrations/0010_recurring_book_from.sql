-- Daueraufträge buchen nur Termine ab book_from (Tag des Anlegens bzw. der
-- letzten Änderung/Fortsetzung). Ältere Termine werden nicht nachgebucht,
-- weil sie in den Kontoständen in der Regel schon enthalten sind.
ALTER TABLE recurring_transactions ADD COLUMN book_from TEXT;
