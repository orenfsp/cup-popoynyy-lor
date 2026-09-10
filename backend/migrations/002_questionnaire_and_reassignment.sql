CREATE TABLE IF NOT EXISTS questionnaire_questions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,
  title_student text NOT NULL,
  title_adult text NOT NULL,
  answer_type text NOT NULL CHECK (answer_type IN ('SINGLE','MULTIPLE','TEXT')),
  options jsonb NOT NULL DEFAULT '[]'::jsonb,
  required boolean NOT NULL DEFAULT false,
  active boolean NOT NULL DEFAULT true,
  sort_order integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reassignment_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  appeal_id uuid NOT NULL REFERENCES appeals(id) ON DELETE CASCADE,
  requested_by text NOT NULL CHECK (requested_by IN ('APPLICANT','EXPERT')),
  reason text NOT NULL,
  status text NOT NULL DEFAULT 'NEW' CHECK (status IN ('NEW','APPROVED','REJECTED')),
  resolved_by uuid REFERENCES staff_users(id),
  resolution_reason text,
  created_at timestamptz NOT NULL DEFAULT now(),
  resolved_at timestamptz
);
CREATE INDEX IF NOT EXISTS reassignment_requests_new_idx ON reassignment_requests(created_at) WHERE status='NEW';

INSERT INTO questionnaire_questions(code,title_student,title_adult,answer_type,options,required,sort_order) VALUES
 ('where_happens','Где это происходит?','Где это происходит?','SINGLE','["В школе","В интернете","Дома","В другом месте"]',false,10),
 ('how_long','Как давно это началось?','Как давно это началось?','SINGLE','["Сегодня","Несколько дней","Несколько недель","Давно"]',false,20),
 ('asked_help','Ты уже обращался за помощью?','Вы уже обращались за помощью?','SINGLE','["Да","Нет","Не хочу отвечать"]',false,30)
ON CONFLICT(code) DO NOTHING;
