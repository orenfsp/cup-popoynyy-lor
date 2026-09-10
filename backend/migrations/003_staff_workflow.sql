ALTER TABLE categories ADD COLUMN IF NOT EXISTS default_expert_id uuid REFERENCES staff_users(id);
ALTER TABLE reassignment_requests ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'TRANSFER' CHECK (kind IN ('TRANSFER','COEXECUTOR'));
ALTER TABLE reassignment_requests ADD COLUMN IF NOT EXISTS requester_id uuid REFERENCES staff_users(id);
CREATE TABLE IF NOT EXISTS staff_work_events (
 id bigserial PRIMARY KEY, staff_id uuid NOT NULL REFERENCES staff_users(id),
 appeal_id uuid REFERENCES appeals(id) ON DELETE SET NULL,
 action text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
