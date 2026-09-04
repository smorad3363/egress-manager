CREATE INDEX idx_operation_journal_finished_retention
    ON operation_journal(updated_at)
    WHERE state IN ('COMMITTED', 'ROLLED_BACK', 'FAILED');
