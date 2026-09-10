ALTER TABLE questionnaire_questions
  ADD COLUMN IF NOT EXISTS show_if_question_code text REFERENCES questionnaire_questions(code) ON UPDATE CASCADE,
  ADD COLUMN IF NOT EXISTS show_if_values jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE questionnaire_questions
  DROP CONSTRAINT IF EXISTS questionnaire_condition_not_self;
ALTER TABLE questionnaire_questions
  ADD CONSTRAINT questionnaire_condition_not_self
  CHECK (show_if_question_code IS NULL OR show_if_question_code <> code);
