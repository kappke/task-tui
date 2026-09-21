ALTER TABLE sync_bases ADD COLUMN created_at TEXT;
UPDATE sync_bases SET created_at = captured_at WHERE created_at IS NULL;
