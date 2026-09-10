-- Chat attachments keep the same sanitised storage as initial screenshots,
-- but are linked to a concrete message so access follows chat visibility.
ALTER TYPE message_author ADD VALUE IF NOT EXISTS 'OPERATOR';

ALTER TABLE appeal_attachments
  ADD COLUMN IF NOT EXISTS message_id uuid REFERENCES appeal_messages(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS author_type message_author NOT NULL DEFAULT 'APPLICANT',
  ADD COLUMN IF NOT EXISTS author_staff_id uuid REFERENCES staff_users(id);

CREATE INDEX IF NOT EXISTS appeal_attachments_message_idx
  ON appeal_attachments(message_id, created_at)
  WHERE message_id IS NOT NULL;
