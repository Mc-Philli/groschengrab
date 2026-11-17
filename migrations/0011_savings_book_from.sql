-- Sparpläne kaufen nur an Terminen ab book_from (Tag des Anlegens bzw. der
-- letzten Änderung/Fortsetzung). Ältere Termine werden nicht nachgekauft,
-- weil die Anteile in der Regel schon erfasst sind.
ALTER TABLE savings_plans ADD COLUMN book_from TEXT;
