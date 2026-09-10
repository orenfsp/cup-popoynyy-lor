CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE staff_role AS ENUM ('OPERATOR', 'EXPERT', 'ADMIN');
CREATE TYPE applicant_type AS ENUM ('STUDENT', 'PARENT', 'TEACHER');
CREATE TYPE appeal_status AS ENUM (
  'NEW', 'ASSIGNED', 'IN_PROGRESS', 'WAITING_FOR_APPLICANT',
  'ANSWER_READY', 'RETURNED', 'COMPLETED', 'REJECTED', 'CLOSED_NO_RESPONSE', 'DELETED'
);
CREATE TYPE appeal_priority AS ENUM ('LOW', 'STANDARD', 'URGENT');
CREATE TYPE message_author AS ENUM ('APPLICANT', 'EXPERT', 'SYSTEM');
CREATE TYPE assignment_kind AS ENUM ('RESPONSIBLE', 'COEXECUTOR');

CREATE TABLE staff_users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  password_hash text NOT NULL,
  display_name text NOT NULL,
  role staff_role NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE staff_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  staff_id uuid NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE categories (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,
  title text NOT NULL,
  active boolean NOT NULL DEFAULT true,
  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE appeals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  applicant_type applicant_type NOT NULL,
  category_id uuid REFERENCES categories(id),
  status appeal_status NOT NULL DEFAULT 'NEW',
  priority appeal_priority NOT NULL DEFAULT 'STANDARD',
  crisis_flag boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX appeals_queue_idx ON appeals (crisis_flag DESC, priority DESC, created_at) WHERE deleted_at IS NULL;

CREATE TABLE appeal_contents (
  appeal_id uuid PRIMARY KEY REFERENCES appeals(id) ON DELETE CASCADE,
  body text NOT NULL,
  initial_answers jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE appeal_access_credentials (
  appeal_id uuid PRIMARY KEY REFERENCES appeals(id) ON DELETE CASCADE,
  public_id text NOT NULL UNIQUE,
  secret_hash bytea NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE anonymous_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE appeal_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  author_type message_author NOT NULL,
  author_staff_id uuid REFERENCES staff_users(id),
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX appeal_messages_timeline_idx ON appeal_messages (appeal_id, created_at);

CREATE TABLE internal_notes (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  author_staff_id uuid NOT NULL REFERENCES staff_users(id),
  body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE appeal_assignments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  expert_id uuid NOT NULL REFERENCES staff_users(id),
  kind assignment_kind NOT NULL,
  active boolean NOT NULL DEFAULT true,
  assigned_by uuid NOT NULL REFERENCES staff_users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz
);
CREATE UNIQUE INDEX one_responsible_expert_idx ON appeal_assignments (appeal_id) WHERE active AND kind = 'RESPONSIBLE';

CREATE TABLE appeal_status_history (
  id bigserial PRIMARY KEY,
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  from_status appeal_status,
  to_status appeal_status NOT NULL,
  actor_staff_id uuid REFERENCES staff_users(id),
  reason text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE applicant_email_channels (
  appeal_id uuid PRIMARY KEY REFERENCES appeals(id) ON DELETE CASCADE,
  encrypted_email bytea NOT NULL,
  email_fingerprint bytea NOT NULL,
  verified_at timestamptz,
  delete_after timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE analytics_facts (
  analytics_id bytea PRIMARY KEY,
  event_day date NOT NULL,
  category_code text NOT NULL,
  applicant_type applicant_type NOT NULL,
  final_status appeal_status,
  priority appeal_priority NOT NULL,
  crisis_flag boolean NOT NULL,
  returned boolean NOT NULL DEFAULT false,
  resolution_seconds bigint,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
  id bigserial PRIMARY KEY,
  actor_staff_id uuid REFERENCES staff_users(id),
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id uuid,
  reason text,
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE outbox_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type text NOT NULL,
  aggregate_id uuid NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  attempts integer NOT NULL DEFAULT 0
);
CREATE INDEX outbox_pending_idx ON outbox_events (created_at) WHERE processed_at IS NULL;

INSERT INTO categories (code, title, sort_order) VALUES
  ('bullying', 'Травля и оскорбления', 10),
  ('classmate_conflict', 'Конфликт с одноклассниками', 20),
  ('cyberbullying', 'Кибербуллинг', 30),
  ('threats', 'Давление и угрозы', 40),
  ('teacher_conflict', 'Конфликт с учителем', 50),
  ('parent_conflict', 'Конфликт с родителями', 60),
  ('legal', 'Вопрос юридического характера', 70),
  ('unknown', 'Не знаю, как это назвать', 80)
ON CONFLICT DO NOTHING;

CREATE TABLE questionnaire_questions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), code text NOT NULL UNIQUE,
  title_student text NOT NULL, title_adult text NOT NULL,
  answer_type text NOT NULL CHECK (answer_type IN ('SINGLE','MULTIPLE','TEXT')),
  options jsonb NOT NULL DEFAULT '[]'::jsonb, required boolean NOT NULL DEFAULT false,
  active boolean NOT NULL DEFAULT true, sort_order integer NOT NULL DEFAULT 0,
  show_if_question_code text REFERENCES questionnaire_questions(code) ON UPDATE CASCADE,
  show_if_values jsonb NOT NULL DEFAULT '[]'::jsonb,
  CONSTRAINT questionnaire_condition_not_self CHECK (show_if_question_code IS NULL OR show_if_question_code <> code),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE reassignment_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  requested_by text NOT NULL CHECK (requested_by IN ('APPLICANT','EXPERT')), reason text NOT NULL,
  status text NOT NULL DEFAULT 'NEW' CHECK (status IN ('NEW','APPROVED','REJECTED')),
  resolved_by uuid REFERENCES staff_users(id), resolution_reason text, created_at timestamptz NOT NULL DEFAULT now(), resolved_at timestamptz
);
CREATE INDEX reassignment_requests_new_idx ON reassignment_requests(created_at) WHERE status='NEW';
INSERT INTO questionnaire_questions(code,title_student,title_adult,answer_type,options,required,sort_order) VALUES
 ('where_happens','Где это происходит?','Где это происходит?','SINGLE','["В школе","В интернете","Дома","В другом месте"]',false,10),
 ('how_long','Как давно это началось?','Как давно это началось?','SINGLE','["Сегодня","Несколько дней","Несколько недель","Давно"]',false,20),
 ('asked_help','Ты уже обращался за помощью?','Вы уже обращались за помощью?','SINGLE','["Да","Нет","Не хочу отвечать"]',false,30)
ON CONFLICT(code) DO NOTHING;

INSERT INTO staff_users (id,email,password_hash,display_name,role) VALUES
  ('00000000-0000-4000-8000-000000000001','operator@molva.local','development-only','Оператор Мария','OPERATOR'),
  ('00000000-0000-4000-8000-000000000002','expert@molva.local','development-only','Психолог Алексей','EXPERT'),
  ('00000000-0000-4000-8000-000000000003','admin@molva.local','development-only','Администратор','ADMIN')
ON CONFLICT DO NOTHING;
