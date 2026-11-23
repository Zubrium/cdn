-- Generic durable operation tasks replace worker-specific task kinds.
DROP INDEX coordination.tasks_claim_idx;

ALTER TABLE coordination.workflows RENAME COLUMN owner_engine_id TO owner_class_id;
ALTER TABLE coordination.tasks RENAME COLUMN component_id TO task_class_id;

DO $$
DECLARE
  constraint_name text;
BEGIN
  FOR constraint_name IN
    SELECT conname
    FROM pg_constraint
    WHERE conrelid = 'coordination.tasks'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%task_kind%'
  LOOP
    EXECUTE format('ALTER TABLE coordination.tasks DROP CONSTRAINT %I', constraint_name);
  END LOOP;
END $$;

UPDATE coordination.tasks SET task_kind = 'operation' WHERE task_kind = 'worker';

ALTER TABLE coordination.tasks
  ADD CONSTRAINT tasks_task_kind_check CHECK (task_kind IN ('operation', 'join')),
  ADD CONSTRAINT tasks_operation_identity_check CHECK (
    (task_kind = 'operation' AND task_class_id IS NOT NULL AND operation_id IS NOT NULL AND contract_version IS NOT NULL)
    OR (task_kind = 'join' AND task_class_id IS NULL AND operation_id IS NULL AND contract_version IS NULL)
  );

CREATE INDEX tasks_claim_idx ON coordination.tasks (state, available_at, created_at)
  WHERE state = 'queued' AND task_kind = 'operation';
