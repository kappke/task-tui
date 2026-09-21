ALTER TABLE sync_operations ADD COLUMN retryable INTEGER NOT NULL DEFAULT 1 CHECK (retryable IN (0, 1));
