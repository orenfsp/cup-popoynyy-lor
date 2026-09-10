ALTER TABLE appeals
  ADD COLUMN IF NOT EXISTS return_count integer NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS accepted_at timestamptz,
  ADD COLUMN IF NOT EXISTS first_response_at timestamptz,
  ADD COLUMN IF NOT EXISTS resolved_at timestamptz;

ALTER TABLE analytics_facts
  ADD COLUMN IF NOT EXISTS acceptance_seconds bigint,
  ADD COLUMN IF NOT EXISTS first_response_seconds bigint;

CREATE TABLE IF NOT EXISTS appeal_attachments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  file_name text NOT NULL,
  content_type text NOT NULL CHECK (content_type IN ('image/jpeg','image/png')),
  byte_size integer NOT NULL CHECK (byte_size > 0 AND byte_size <= 10485760),
  content bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS appeal_attachments_appeal_idx ON appeal_attachments(appeal_id, created_at);

CREATE TABLE IF NOT EXISTS appeal_feedback (
  appeal_id uuid PRIMARY KEY REFERENCES appeals(id) ON DELETE CASCADE,
  helpful boolean,
  rating smallint CHECK (rating BETWEEN 1 AND 5),
  comment text CHECK (char_length(comment) <= 2000),
  complaint boolean NOT NULL DEFAULT false,
  complaint_text text CHECK (char_length(complaint_text) <= 2000),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS specialist_groups (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL UNIQUE,
  max_active_per_expert integer NOT NULL DEFAULT 20 CHECK (max_active_per_expert BETWEEN 1 AND 500),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS specialist_group_members (
  group_id uuid NOT NULL REFERENCES specialist_groups(id) ON DELETE CASCADE,
  expert_id uuid NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
  PRIMARY KEY(group_id, expert_id)
);
CREATE TABLE IF NOT EXISTS category_routing_rules (
  category_id uuid PRIMARY KEY REFERENCES categories(id) ON DELETE CASCADE,
  group_id uuid NOT NULL REFERENCES specialist_groups(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO specialist_groups(name,max_active_per_expert)
VALUES ('Основная группа',20)
ON CONFLICT(name) DO NOTHING;

INSERT INTO specialist_group_members(group_id,expert_id)
SELECT g.id,u.id FROM specialist_groups g CROSS JOIN staff_users u
WHERE g.name='Основная группа' AND u.role='EXPERT'
ON CONFLICT DO NOTHING;

INSERT INTO category_routing_rules(category_id,group_id)
SELECT c.id,g.id FROM categories c CROSS JOIN specialist_groups g
WHERE g.name='Основная группа' AND c.default_expert_id IS NOT NULL
ON CONFLICT(category_id) DO NOTHING;
