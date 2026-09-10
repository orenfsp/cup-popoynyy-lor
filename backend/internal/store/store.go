package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	Pool   *pgxpool.Pool
	Pepper []byte
}

type CreateAppealInput struct {
	ApplicantType string
	CategoryCode  string
	Body          string
	Answers       map[string]any
	Email         string
	CrisisFlag    bool
	Attachments   []AttachmentInput
}

type AttachmentInput struct {
	FileName    string
	ContentType string
	Data        []byte
}

type Attachment struct {
	ID          string    `json:"id"`
	FileName    string    `json:"fileName"`
	ContentType string    `json:"contentType"`
	ByteSize    int       `json:"byteSize"`
	CreatedAt   time.Time `json:"createdAt"`
}

type StatusEvent struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Appeal struct {
	ID            string         `json:"id"`
	PublicID      string         `json:"publicId"`
	ApplicantType string         `json:"applicantType"`
	CategoryCode  string         `json:"categoryCode"`
	CategoryTitle string         `json:"categoryTitle"`
	Body          string         `json:"body"`
	Answers       map[string]any `json:"answers"`
	Status        string         `json:"status"`
	Priority      string         `json:"priority"`
	CrisisFlag    bool           `json:"crisisFlag"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	ReturnCount   int            `json:"returnCount"`
	StatusHistory []StatusEvent  `json:"statusHistory"`
	Attachments   []Attachment   `json:"attachments"`
	Feedback      map[string]any `json:"feedback,omitempty"`
}

type Message struct {
	ID          string       `json:"id"`
	AuthorType  string       `json:"authorType"`
	AuthorName  string       `json:"authorName"`
	Body        string       `json:"body"`
	CreatedAt   time.Time    `json:"createdAt"`
	Attachments []Attachment `json:"attachments"`
}
type Question struct {
	ID                 string   `json:"id"`
	Code               string   `json:"code"`
	TitleStudent       string   `json:"titleStudent"`
	TitleAdult         string   `json:"titleAdult"`
	AnswerType         string   `json:"answerType"`
	Options            []string `json:"options"`
	Required           bool     `json:"required"`
	Active             bool     `json:"active"`
	SortOrder          int      `json:"sortOrder"`
	ShowIfQuestionCode string   `json:"showIfQuestionCode"`
	ShowIfValues       []string `json:"showIfValues"`
}

type AnalyticsRow struct {
	Day                         time.Time `json:"day"`
	Category                    string    `json:"category"`
	Count                       int64     `json:"count"`
	UrgentCount                 int64     `json:"urgentCount"`
	ReturnedCount               int64     `json:"returnedCount"`
	DeletedCount                int64     `json:"deletedCount"`
	AverageAcceptanceSeconds    int64     `json:"averageAcceptanceSeconds"`
	AverageFirstResponseSeconds int64     `json:"averageFirstResponseSeconds"`
	AverageResolutionSeconds    int64     `json:"averageResolutionSeconds"`
}

type StaffUser struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
}

type StaffAppeal struct {
	ID                 string    `json:"id"`
	PublicID           string    `json:"publicId"`
	ApplicantType      string    `json:"applicantType"`
	CategoryCode       string    `json:"categoryCode"`
	CategoryTitle      string    `json:"categoryTitle"`
	Body               string    `json:"body,omitempty"`
	Status             string    `json:"status"`
	Priority           string    `json:"priority"`
	CrisisFlag         bool      `json:"crisisFlag"`
	CreatedAt          time.Time `json:"createdAt"`
	ReassignmentReason string    `json:"reassignmentReason,omitempty"`
}

func New(ctx context.Context, databaseURL string, pepper []byte) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool, Pepper: pepper}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) HashSecret(secret string) []byte {
	h := hmac.New(sha256.New, s.Pepper)
	h.Write([]byte(secret))
	return h.Sum(nil)
}

func (s *Store) CreateAppeal(ctx context.Context, in CreateAppealInput, publicID, secret string) (Appeal, error) {
	questions, err := s.Questions(ctx, true)
	if err != nil {
		return Appeal{}, err
	}
	cleanAnswers := make(map[string]any)
	shown := make(map[string]bool)
	for _, q := range questions {
		visible := q.ShowIfQuestionCode == "" || (shown[q.ShowIfQuestionCode] && answerMatches(in.Answers[q.ShowIfQuestionCode], q.ShowIfValues))
		if !visible {
			continue
		}
		shown[q.Code] = true
		v, ok := in.Answers[q.Code]
		if !ok || v == nil || v == "" {
			if q.Required {
				return Appeal{}, ErrInvalid
			}
			continue
		}
		valid := func(v any) bool {
			t, ok := v.(string)
			if !ok {
				return false
			}
			for _, o := range q.Options {
				if t == o {
					return true
				}
			}
			return false
		}
		switch q.AnswerType {
		case "TEXT":
			t, ok := v.(string)
			if !ok || len(t) > 2000 || (q.Required && strings.TrimSpace(t) == "") {
				return Appeal{}, ErrInvalid
			}
		case "SINGLE":
			if !valid(v) {
				return Appeal{}, ErrInvalid
			}
		case "MULTIPLE":
			vv, ok := v.([]any)
			if !ok || (q.Required && len(vv) == 0) {
				return Appeal{}, ErrInvalid
			}
			for _, x := range vv {
				if !valid(x) {
					return Appeal{}, ErrInvalid
				}
			}
		}
		cleanAnswers[q.Code] = v
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Appeal{}, err
	}
	defer tx.Rollback(ctx)

	answers, err := json.Marshal(cleanAnswers)
	if err != nil {
		return Appeal{}, err
	}
	var appeal Appeal
	err = tx.QueryRow(ctx, `
		INSERT INTO appeals (applicant_type, category_id, crisis_flag)
		SELECT $1, id, $3 FROM categories WHERE code = $2 AND active
		RETURNING id, applicant_type, status, priority, crisis_flag, created_at, updated_at`,
		in.ApplicantType, in.CategoryCode, in.CrisisFlag,
	).Scan(&appeal.ID, &appeal.ApplicantType, &appeal.Status, &appeal.Priority, &appeal.CrisisFlag, &appeal.CreatedAt, &appeal.UpdatedAt)
	if err != nil {
		return Appeal{}, fmt.Errorf("create appeal: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO appeal_contents (appeal_id, body, initial_answers) VALUES ($1,$2,$3)`, appeal.ID, in.Body, answers)
	if err != nil {
		return Appeal{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO appeal_access_credentials (appeal_id, public_id, secret_hash) VALUES ($1,$2,$3)`, appeal.ID, publicID, s.HashSecret(secret))
	if err != nil {
		return Appeal{}, err
	}
	if in.Email != "" {
		encryptedEmail, fingerprint, protectErr := s.protectApplicantEmail(in.Email)
		if protectErr != nil {
			return Appeal{}, protectErr
		}
		_, err = tx.Exec(ctx, `INSERT INTO applicant_email_channels (appeal_id, encrypted_email, email_fingerprint) VALUES ($1,$2,$3)`, appeal.ID, encryptedEmail, fingerprint)
		if err != nil {
			return Appeal{}, err
		}
	}
	for _, file := range in.Attachments {
		_, err = tx.Exec(ctx, `INSERT INTO appeal_attachments(appeal_id,file_name,content_type,byte_size,content) VALUES($1,$2,$3,$4,$5)`, appeal.ID, file.FileName, file.ContentType, len(file.Data), file.Data)
		if err != nil {
			return Appeal{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO appeal_status_history (appeal_id, to_status) VALUES ($1,'NEW')`, appeal.ID)
	if err != nil {
		return Appeal{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_id, payload) VALUES ('appeal.created',$1::uuid,jsonb_build_object('appealId',$2::text))`, appeal.ID, appeal.ID)
	if err != nil {
		return Appeal{}, err
	}
	// Routing stays advisory: every new appeal first reaches an operator.
	if err := tx.Commit(ctx); err != nil {
		return Appeal{}, err
	}
	appeal.PublicID = publicID
	appeal.CategoryCode = in.CategoryCode
	appeal.Body = in.Body
	appeal.Answers = cleanAnswers
	return appeal, nil
}

func (s *Store) AuthenticateAppeal(ctx context.Context, accessCode string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `
		SELECT a.id FROM appeals a
		JOIN appeal_access_credentials c ON c.appeal_id=a.id
		WHERE c.secret_hash=$1 AND c.revoked_at IS NULL AND a.deleted_at IS NULL`,
		s.HashSecret(accessCode),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) ListStaff(ctx context.Context) ([]StaffUser, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,email,display_name,role,active,created_at FROM staff_users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StaffUser, 0)
	for rows.Next() {
		var u StaffUser
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Active, &u.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) ListExperts(ctx context.Context) ([]StaffUser, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,email,display_name,role,active,created_at FROM staff_users WHERE role='EXPERT' AND active ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]StaffUser, 0)
	for rows.Next() {
		var u StaffUser
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Active, &u.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) CreateStaff(ctx context.Context, email, name, role string) (StaffUser, error) {
	if role != "OPERATOR" && role != "EXPERT" {
		return StaffUser{}, errors.New("invalid role")
	}
	var u StaffUser
	err := s.Pool.QueryRow(ctx, `INSERT INTO staff_users(email,password_hash,display_name,role) VALUES(lower($1),'invite-pending',$2,$3) RETURNING id,email,display_name,role,active,created_at`, strings.TrimSpace(email), strings.TrimSpace(name), role).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Active, &u.CreatedAt)
	return u, err
}

func (s *Store) CreateAnonymousSession(ctx context.Context, appealID string, tokenHash []byte, expires time.Time) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO anonymous_sessions (appeal_id, token_hash, expires_at) VALUES ($1,$2,$3)`, appealID, tokenHash, expires)
	return err
}

func (s *Store) AppealIDBySession(ctx context.Context, token string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT appeal_id FROM anonymous_sessions WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()`, s.HashSecret(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) GetAppeal(ctx context.Context, appealID string) (Appeal, []Message, error) {
	var a Appeal
	var answers []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT a.id,c.public_id,a.applicant_type,cat.code,cat.title,ac.body,ac.initial_answers,
		       a.status,a.priority,a.crisis_flag,a.created_at,a.updated_at,a.return_count
		FROM appeals a
		JOIN appeal_access_credentials c ON c.appeal_id=a.id
		JOIN appeal_contents ac ON ac.appeal_id=a.id
		JOIN categories cat ON cat.id=a.category_id
		WHERE a.id=$1 AND a.deleted_at IS NULL`, appealID,
	).Scan(&a.ID, &a.PublicID, &a.ApplicantType, &a.CategoryCode, &a.CategoryTitle, &a.Body, &answers, &a.Status, &a.Priority, &a.CrisisFlag, &a.CreatedAt, &a.UpdatedAt, &a.ReturnCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Appeal{}, nil, ErrNotFound
	}
	if err != nil {
		return Appeal{}, nil, err
	}
	if err := json.Unmarshal(answers, &a.Answers); err != nil {
		return Appeal{}, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT m.id,m.author_type,COALESCE(u.display_name,CASE WHEN m.author_type='APPLICANT' THEN 'Вы' ELSE 'Молва' END),m.body,m.created_at FROM appeal_messages m LEFT JOIN staff_users u ON u.id=m.author_staff_id WHERE m.appeal_id=$1 ORDER BY m.created_at`, appealID)
	if err != nil {
		return Appeal{}, nil, err
	}
	defer rows.Close()
	messages := make([]Message, 0)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.AuthorType, &m.AuthorName, &m.Body, &m.CreatedAt); err != nil {
			return Appeal{}, nil, err
		}
		attachmentRows, attachmentErr := s.Pool.Query(ctx, `SELECT id,file_name,content_type,byte_size,created_at FROM appeal_attachments WHERE message_id=$1 ORDER BY created_at`, m.ID)
		if attachmentErr != nil {
			return Appeal{}, nil, attachmentErr
		}
		m.Attachments, attachmentErr = pgx.CollectRows(attachmentRows, pgx.RowToStructByPos[Attachment])
		if attachmentErr != nil {
			return Appeal{}, nil, attachmentErr
		}
		messages = append(messages, m)
	}
	if err = rows.Err(); err != nil {
		return Appeal{}, nil, err
	}
	historyRows, err := s.Pool.Query(ctx, `SELECT to_status,COALESCE(reason,''),created_at FROM appeal_status_history WHERE appeal_id=$1 ORDER BY created_at`, appealID)
	if err != nil {
		return Appeal{}, nil, err
	}
	a.StatusHistory, err = pgx.CollectRows(historyRows, pgx.RowToStructByPos[StatusEvent])
	if err != nil {
		return Appeal{}, nil, err
	}
	attachmentRows, err := s.Pool.Query(ctx, `SELECT id,file_name,content_type,byte_size,created_at FROM appeal_attachments WHERE appeal_id=$1 AND message_id IS NULL ORDER BY created_at`, appealID)
	if err != nil {
		return Appeal{}, nil, err
	}
	a.Attachments, err = pgx.CollectRows(attachmentRows, pgx.RowToStructByPos[Attachment])
	if err != nil {
		return Appeal{}, nil, err
	}
	var feedback map[string]any
	err = s.Pool.QueryRow(ctx, `SELECT jsonb_build_object('helpful',helpful,'rating',rating,'comment',comment,'complaint',complaint) FROM appeal_feedback WHERE appeal_id=$1`, appealID).Scan(&feedback)
	if err == nil {
		a.Feedback = feedback
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Appeal{}, nil, err
	}
	return a, messages, nil
}

func (s *Store) ApplicantAttachment(ctx context.Context, appealID, attachmentID string) (string, string, []byte, error) {
	var name, contentType string
	var data []byte
	err := s.Pool.QueryRow(ctx, `SELECT file_name,content_type,content FROM appeal_attachments WHERE id=$1 AND appeal_id=$2`, attachmentID, appealID).Scan(&name, &contentType, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrNotFound
	}
	return name, contentType, data, err
}

func (s *Store) Questions(ctx context.Context, onlyActive bool) ([]Question, error) {
	query := `SELECT id,code,title_student,title_adult,answer_type,options,required,active,sort_order,COALESCE(show_if_question_code,''),show_if_values FROM questionnaire_questions`
	if onlyActive {
		query += ` WHERE active`
	}
	query += ` ORDER BY sort_order,created_at`
	rows, err := s.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Question, 0)
	for rows.Next() {
		var q Question
		var raw, conditions []byte
		if err := rows.Scan(&q.ID, &q.Code, &q.TitleStudent, &q.TitleAdult, &q.AnswerType, &raw, &q.Required, &q.Active, &q.SortOrder, &q.ShowIfQuestionCode, &conditions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &q.Options); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(conditions, &q.ShowIfValues); err != nil {
			return nil, err
		}
		result = append(result, q)
	}
	return result, rows.Err()
}
func (s *Store) CreateQuestion(ctx context.Context, q Question) (Question, error) {
	raw, err := json.Marshal(q.Options)
	if err != nil {
		return Question{}, err
	}
	conditions, err := json.Marshal(q.ShowIfValues)
	if err != nil {
		return Question{}, err
	}
	if err = s.ValidateQuestionCondition(ctx, q.Code, q.ShowIfQuestionCode, q.ShowIfValues, q.SortOrder, ""); err != nil {
		return Question{}, err
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO questionnaire_questions(code,title_student,title_adult,answer_type,options,required,active,sort_order,show_if_question_code,show_if_values) VALUES($1,$2,$3,$4,$5,$6,$8,$7,NULLIF($9,''),$10) RETURNING id,active`, q.Code, q.TitleStudent, q.TitleAdult, q.AnswerType, raw, q.Required, q.SortOrder, q.Active, q.ShowIfQuestionCode, conditions).Scan(&q.ID, &q.Active)
	return q, err
}
func (s *Store) ToggleQuestion(ctx context.Context, id string, active bool) error {
	_, err := s.Pool.Exec(ctx, `UPDATE questionnaire_questions SET active=$2,updated_at=now() WHERE id=$1`, id, active)
	return err
}
func (s *Store) RequestReassignment(ctx context.Context, appealID, reason string) error {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return errors.New("reason required")
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO reassignment_requests(appeal_id,requested_by,reason) VALUES($1,'APPLICANT',$2)`, appealID, reason)
	return err
}

func answerMatches(answer any, values []string) bool {
	if len(values) == 0 {
		return false
	}
	matches := func(v string) bool {
		for _, want := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	switch v := answer.(type) {
	case string:
		return matches(v)
	case []any:
		for _, x := range v {
			if text, ok := x.(string); ok && matches(text) {
				return true
			}
		}
	case []string:
		for _, x := range v {
			if matches(x) {
				return true
			}
		}
	}
	return false
}

func (s *Store) ValidateQuestionCondition(ctx context.Context, code, parent string, values []string, sortOrder int, id string) error {
	if parent == "" {
		if len(values) > 0 {
			return ErrInvalid
		}
		return nil
	}
	if parent == code || len(values) == 0 {
		return ErrInvalid
	}
	var answerType string
	var raw []byte
	var parentOrder int
	if err := s.Pool.QueryRow(ctx, `SELECT answer_type,options,sort_order FROM questionnaire_questions WHERE code=$1 AND ($2='' OR id<>$2::uuid)`, parent, id).Scan(&answerType, &raw, &parentOrder); err != nil {
		return ErrInvalid
	}
	if parentOrder >= sortOrder {
		return ErrInvalid
	}
	if answerType == "TEXT" {
		return ErrInvalid
	}
	var options []string
	if json.Unmarshal(raw, &options) != nil {
		return ErrInvalid
	}
	for _, v := range values {
		found := false
		for _, o := range options {
			if v == o {
				found = true
			}
		}
		if !found {
			return ErrInvalid
		}
	}
	var cycle bool
	err := s.Pool.QueryRow(ctx, `WITH RECURSIVE chain(code,parent) AS (SELECT code,show_if_question_code FROM questionnaire_questions WHERE code=$1 UNION ALL SELECT q.code,q.show_if_question_code FROM questionnaire_questions q JOIN chain c ON q.code=c.parent WHERE c.parent IS NOT NULL) SELECT EXISTS(SELECT 1 FROM chain WHERE code=$2)`, parent, code).Scan(&cycle)
	if err != nil || cycle {
		return ErrInvalid
	}
	return nil
}

func (s *Store) AddApplicantMessage(ctx context.Context, appealID, body string, attachments []AttachmentInput, crisis bool) (Message, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback(ctx)
	var m Message
	err = tx.QueryRow(ctx, `
		INSERT INTO appeal_messages (appeal_id,author_type,body)
		SELECT $1,'APPLICANT',$2 FROM appeals WHERE id=$1 AND deleted_at IS NULL AND status NOT IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE')
		RETURNING id,author_type,body,created_at`, appealID, body,
	).Scan(&m.ID, &m.AuthorType, &m.Body, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrForbidden
	}
	if err != nil {
		return m, err
	}
	for _, file := range attachments {
		var attachment Attachment
		err = tx.QueryRow(ctx, `INSERT INTO appeal_attachments(appeal_id,message_id,author_type,file_name,content_type,byte_size,content) VALUES($1,$2,'APPLICANT',$3,$4,$5,$6) RETURNING id,file_name,content_type,byte_size,created_at`, appealID, m.ID, file.FileName, file.ContentType, len(file.Data), file.Data).Scan(&attachment.ID, &attachment.FileName, &attachment.ContentType, &attachment.ByteSize, &attachment.CreatedAt)
		if err != nil {
			return m, err
		}
		m.Attachments = append(m.Attachments, attachment)
	}
	if crisis {
		_, err = tx.Exec(ctx, `UPDATE appeals SET crisis_flag=true,updated_at=now() WHERE id=$1`, appealID)
		if err != nil {
			return m, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES('appeal.message_added',$1,'{}')`, appealID)
	if err != nil {
		return m, err
	}
	err = tx.Commit(ctx)
	return m, err
}

func (s *Store) DeleteAppeal(ctx context.Context, appealID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE appeals SET status='DELETED',deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE appeal_access_credentials SET revoked_at=now() WHERE appeal_id=$1`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE anonymous_sessions SET revoked_at=now() WHERE appeal_id=$1`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM applicant_email_channels WHERE appeal_id=$1`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM reassignment_requests WHERE appeal_id=$1`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE staff_work_events SET appeal_id=NULL WHERE appeal_id=$1`, appealID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES ('appeal.deletion_requested',$1,'{}')`, appealID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Analytics(ctx context.Context, k int) ([]AnalyticsRow, error) {
	query := `SELECT event_day,category_code,count(*),count(*) FILTER (WHERE priority='URGENT'),count(*) FILTER (WHERE returned),count(*) FILTER (WHERE final_status='DELETED'),COALESCE(avg(acceptance_seconds),0)::bigint,COALESCE(avg(first_response_seconds),0)::bigint,COALESCE(avg(resolution_seconds),0)::bigint FROM analytics_facts GROUP BY event_day,category_code HAVING count(*) >= $1 ORDER BY event_day DESC,category_code`
	rows, err := s.Pool.Query(ctx, query, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AnalyticsRow, 0)
	for rows.Next() {
		var r AnalyticsRow
		if err := rows.Scan(&r.Day, &r.Category, &r.Count, &r.UrgentCount, &r.ReturnedCount, &r.DeletedCount, &r.AverageAcceptanceSeconds, &r.AverageFirstResponseSeconds, &r.AverageResolutionSeconds); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) AnalyticsOverview(ctx context.Context, k int) (map[string]any, error) {
	result := map[string]any{}
	var total, urgent, returned int64
	var avgAcceptance, avgFirstResponse, avgResolution int64
	err := s.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE priority='URGENT'),count(*) FILTER(WHERE returned),COALESCE(avg(acceptance_seconds),0)::bigint,COALESCE(avg(first_response_seconds),0)::bigint,COALESCE(avg(resolution_seconds),0)::bigint FROM analytics_facts`).Scan(&total, &urgent, &returned, &avgAcceptance, &avgFirstResponse, &avgResolution)
	if err != nil {
		return nil, err
	}
	result["summary"] = map[string]any{"total": total, "urgent": urgent, "returned": returned, "averageAcceptanceSeconds": avgAcceptance, "averageFirstResponseSeconds": avgFirstResponse, "averageResolutionSeconds": avgResolution}
	queries := map[string]string{
		"byApplicantType": `SELECT applicant_type AS label,count(*) AS count FROM analytics_facts GROUP BY applicant_type HAVING count(*) >= $1 ORDER BY count DESC`,
		"byStatus":        `SELECT COALESCE(final_status::text,'NEW') AS label,count(*) AS count FROM analytics_facts GROUP BY final_status HAVING count(*) >= $1 ORDER BY count DESC`,
		"staffLoad":       `SELECT u.display_name AS label,u.role,count(e.id) AS count FROM staff_users u LEFT JOIN staff_work_events e ON e.staff_id=u.id WHERE u.role IN ('OPERATOR','EXPERT') GROUP BY u.id,u.display_name,u.role ORDER BY count DESC`,
	}
	for key, query := range queries {
		args := []any{}
		if key != "staffLoad" {
			args = append(args, k)
		}
		items, queryErr := s.Maps(ctx, query, args...)
		if queryErr != nil {
			return nil, queryErr
		}
		result[key] = items
	}
	return result, nil
}

func (s *Store) protectApplicantEmail(email string) ([]byte, []byte, error) {
	key := sha256.Sum256(append([]byte("molva-applicant-email:"), s.Pepper...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	encrypted := append(nonce, gcm.Seal(nil, nonce, []byte(email), nil)...)
	fingerprintMAC := hmac.New(sha256.New, append([]byte("molva-applicant-email-fingerprint:"), s.Pepper...))
	fingerprintMAC.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	return encrypted, fingerprintMAC.Sum(nil), nil
}
