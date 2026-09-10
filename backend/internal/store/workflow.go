package store

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

var ErrForbidden = errors.New("forbidden")
var ErrInvalid = errors.New("invalid operation")

func (s *Store) SetStaffPassword(ctx context.Context, id, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE staff_users SET password_hash=$2 WHERE id=$1`, id, string(hash))
	return err
}

type Identity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func (s *Store) Login(ctx context.Context, email, password, token string) (Identity, error) {
	var u Identity
	var hash string
	err := s.Pool.QueryRow(ctx, `SELECT id,display_name,role,password_hash FROM staff_users WHERE lower(email)=lower($1) AND active`, email).Scan(&u.ID, &u.Name, &u.Role, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return u, ErrForbidden
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO staff_sessions(staff_id,token_hash,expires_at) VALUES($1,$2,$3)`, u.ID, s.HashSecret(token), time.Now().Add(12*time.Hour))
	return u, err
}
func (s *Store) StaffSession(ctx context.Context, token string) (Identity, error) {
	var u Identity
	err := s.Pool.QueryRow(ctx, `SELECT u.id,u.display_name,u.role FROM staff_users u JOIN staff_sessions s ON s.staff_id=u.id WHERE s.token_hash=$1 AND s.expires_at>now() AND u.active`, s.HashSecret(token)).Scan(&u.ID, &u.Name, &u.Role)
	return u, err
}
func (s *Store) BootstrapStaff(ctx context.Context, password string) error {
	if len(password) < 12 {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE staff_users SET password_hash=$1 WHERE password_hash='development-only'`, string(hash))
	return err
}

// Separate projections prevent administrator and operator content leakage.
func (s *Store) Cases(ctx context.Context, u Identity) ([]map[string]any, error) {
	query := `SELECT a.id,c.public_id,a.status,a.priority,cat.title AS category,cat.id AS category_id,a.created_at,a.crisis_flag,
 EXTRACT(EPOCH FROM (now()-a.created_at))::bigint AS waiting_seconds,
 (a.status='NEW' AND a.created_at < now()-interval '4 hours') AS overdue,
 (a.status NOT IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE') AND a.updated_at < now()-interval '24 hours') AS stale,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',x.expert_id,'name',su.display_name,'kind',x.kind)) FROM appeal_assignments x JOIN staff_users su ON su.id=x.expert_id WHERE x.appeal_id=a.id AND x.active),'[]') AS experts
 FROM appeals a JOIN appeal_access_credentials c ON c.appeal_id=a.id JOIN categories cat ON cat.id=a.category_id WHERE a.deleted_at IS NULL`
	var args []any
	if u.Role == "EXPERT" {
		query += ` AND EXISTS(SELECT 1 FROM appeal_assignments x WHERE x.appeal_id=a.id AND x.expert_id=$1 AND x.active)`
		args = append(args, u.ID)
	}
	// Overdue and stale work is always raised; within a group the oldest item stays
	// above newer ones (so newly received appeals naturally appear lower).
	query += ` ORDER BY overdue DESC,stale DESC,a.crisis_flag DESC,CASE a.priority WHEN 'URGENT' THEN 3 WHEN 'STANDARD' THEN 2 ELSE 1 END DESC,a.created_at ASC`
	return s.Maps(ctx, query, args...)
}
func (s *Store) Maps(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT row_to_json(result) FROM (`+q+`) result`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var item map[string]any
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func authorizeCase(ctx context.Context, tx pgx.Tx, u Identity, id string) error {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM appeals a WHERE a.id=$1 AND a.deleted_at IS NULL AND ($2!='EXPERT' OR EXISTS(SELECT 1 FROM appeal_assignments x WHERE x.appeal_id=a.id AND x.expert_id=$3::uuid AND x.active)))`, id, u.Role, u.ID).Scan(&found)
	if err != nil {
		return err
	}
	if !found {
		return ErrForbidden
	}
	return nil
}
func (s *Store) CaseDetail(ctx context.Context, u Identity, id string) (map[string]any, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id FROM appeals WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&locked); err != nil {
		return nil, ErrNotFound
	}
	if err = authorizeCase(ctx, tx, u, id); err != nil {
		return nil, err
	}
	result := map[string]any{}
	result["caseId"] = id
	if u.Role != "ADMIN" {
		var body string
		var answers any
		err = tx.QueryRow(ctx, `SELECT body,initial_answers FROM appeal_contents WHERE appeal_id=$1`, id).Scan(&body, &answers)
		if err != nil {
			return nil, err
		}
		result["body"] = body
		result["answers"] = answers
		answerRows, e := tx.Query(ctx, `SELECT q.code,CASE WHEN a.applicant_type='STUDENT' THEN q.title_student ELSE q.title_adult END AS question,ac.initial_answers->q.code AS answer FROM appeals a JOIN appeal_contents ac ON ac.appeal_id=a.id JOIN questionnaire_questions q ON ac.initial_answers ? q.code WHERE a.id=$1 ORDER BY q.sort_order,q.created_at`, id)
		if e != nil {
			return nil, e
		}
		result["answerItems"], err = pgx.CollectRows(answerRows, pgx.RowToMap)
		if err != nil {
			return nil, err
		}
		rows, e := tx.Query(ctx, `SELECT id::text,file_name,content_type,byte_size,created_at FROM appeal_attachments WHERE appeal_id=$1 AND message_id IS NULL ORDER BY created_at`, id)
		if e != nil {
			return nil, e
		}
		result["attachments"], err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			return nil, err
		}
	}
	canChat := u.Role == "EXPERT"
	if u.Role == "OPERATOR" {
		err = tx.QueryRow(ctx, `SELECT a.status IN ('NEW','RETURNED') AND NOT EXISTS(SELECT 1 FROM appeal_assignments aa WHERE aa.appeal_id=a.id AND aa.active AND aa.kind='RESPONSIBLE') FROM appeals a WHERE a.id=$1`, id).Scan(&canChat)
		if err != nil {
			return nil, err
		}
	}
	result["canChat"] = canChat
	if canChat {
		rows, e := tx.Query(ctx, `SELECT m.id::text,m.body,m.author_type,COALESCE(u.display_name,'Заявитель') AS author_name,m.created_at,COALESCE((SELECT jsonb_agg(jsonb_build_object('id',f.id,'file_name',f.file_name,'content_type',f.content_type,'byte_size',f.byte_size) ORDER BY f.created_at) FROM appeal_attachments f WHERE f.message_id=m.id),'[]') AS attachments FROM appeal_messages m LEFT JOIN staff_users u ON u.id=m.author_staff_id WHERE m.appeal_id=$1 ORDER BY m.created_at`, id)
		if e != nil {
			return nil, e
		}
		result["messages"], err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			return nil, err
		}
	}
	if u.Role == "EXPERT" {
		rows, e := tx.Query(ctx, `SELECT n.id::text,n.body,u.display_name AS author_name,n.created_at FROM internal_notes n JOIN staff_users u ON u.id=n.author_staff_id WHERE n.appeal_id=$1 ORDER BY n.created_at`, id)
		if e != nil {
			return nil, e
		}
		result["notes"], err = pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			return nil, err
		}
	}
	if u.Role == "OPERATOR" {
		var feedback map[string]any
		err = tx.QueryRow(ctx, `SELECT jsonb_build_object('helpful',helpful,'rating',rating,'comment',comment,'complaint',complaint,'complaintText',complaint_text) FROM appeal_feedback WHERE appeal_id=$1`, id).Scan(&feedback)
		if err == nil {
			result["feedback"] = feedback
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var routing map[string]any
	err = tx.QueryRow(ctx, `SELECT row_to_json(hint) FROM (
		SELECT g.id AS group_id,g.name AS group_name,g.max_active_per_expert,
		       u.id AS expert_id,u.display_name AS expert_name,
		       (SELECT count(*) FROM appeal_assignments aa JOIN appeals ap ON ap.id=aa.appeal_id WHERE aa.expert_id=u.id AND aa.active AND ap.status NOT IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE','DELETED')) AS active_count
		FROM appeals a
		JOIN category_routing_rules rr ON rr.category_id=a.category_id
		JOIN specialist_groups g ON g.id=rr.group_id AND g.active
		LEFT JOIN LATERAL (
			SELECT su.id,su.display_name FROM specialist_group_members gm JOIN staff_users su ON su.id=gm.expert_id AND su.active AND su.role='EXPERT'
			WHERE gm.group_id=g.id ORDER BY (SELECT count(*) FROM appeal_assignments aa JOIN appeals ap ON ap.id=aa.appeal_id WHERE aa.expert_id=su.id AND aa.active AND ap.status NOT IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE','DELETED')),su.display_name LIMIT 1
		) u ON true WHERE a.id=$1
	) hint`, id).Scan(&routing)
	if err == nil {
		if count, ok := routing["active_count"].(float64); ok {
			if max, ok := routing["max_active_per_expert"].(float64); ok && count >= max {
				routing["overloaded"] = true
				routing["expert_id"] = nil
				routing["expert_name"] = nil
			}
		}
		result["routingHint"] = routing
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	historyRows, e := tx.Query(ctx, `SELECT to_status,COALESCE(reason,''),created_at FROM appeal_status_history WHERE appeal_id=$1 ORDER BY created_at`, id)
	if e != nil {
		return nil, e
	}
	result["statusHistory"], err = pgx.CollectRows(historyRows, pgx.RowToMap)
	if err != nil {
		return nil, err
	}
	// Admin gets operational request metadata, never free-text reasons.
	query := `SELECT id::text,kind,status,requested_by,created_at FROM reassignment_requests WHERE appeal_id=$1 ORDER BY created_at DESC`
	if u.Role != "ADMIN" {
		query = `SELECT id::text,kind,status,requested_by,reason,created_at FROM reassignment_requests WHERE appeal_id=$1 ORDER BY created_at DESC`
	}
	rows, e := tx.Query(ctx, query, id)
	if e != nil {
		return nil, e
	}
	result["requests"], err = pgx.CollectRows(rows, pgx.RowToMap)
	return result, err
}

type Action struct {
	Action    string `json:"action"`
	Value     string `json:"value"`
	Body      string `json:"body"`
	ExpertID  string `json:"expertId"`
	RequestID string `json:"requestId"`
}

func (s *Store) AddStaffMessage(ctx context.Context, u Identity, id, body string, attachments []AttachmentInput) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM appeals WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&status); err != nil {
		return ErrNotFound
	}
	if err = authorizeCase(ctx, tx, u, id); err != nil {
		return err
	}
	if status == "COMPLETED" || status == "REJECTED" || status == "CLOSED_NO_RESPONSE" {
		return ErrForbidden
	}
	if u.Role == "OPERATOR" {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT $2::text IN ('NEW','RETURNED') AND NOT EXISTS(SELECT 1 FROM appeal_assignments WHERE appeal_id=$1 AND active AND kind='RESPONSIBLE')`, id, status).Scan(&allowed)
		if err != nil || !allowed {
			return ErrForbidden
		}
	} else if u.Role != "EXPERT" {
		return ErrForbidden
	}
	authorType := u.Role
	var messageID string
	err = tx.QueryRow(ctx, `INSERT INTO appeal_messages(appeal_id,author_type,author_staff_id,body) VALUES($1,$2::message_author,$3,$4) RETURNING id`, id, authorType, u.ID, body).Scan(&messageID)
	if err != nil {
		return err
	}
	for _, file := range attachments {
		_, err = tx.Exec(ctx, `INSERT INTO appeal_attachments(appeal_id,message_id,author_type,author_staff_id,file_name,content_type,byte_size,content) VALUES($1,$2,$3::message_author,$4,$5,$6,$7,$8)`, id, messageID, authorType, u.ID, file.FileName, file.ContentType, len(file.Data), file.Data)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE appeals SET first_response_at=COALESCE(first_response_at,now()),updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO staff_work_events(staff_id,appeal_id,action) VALUES($1,$2,'reply')`, u.ID, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_staff_id,action,resource_type,resource_id) VALUES($1,'reply','appeal',$2)`, u.ID, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES('appeal.message_added',$1,'{}')`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func AllowedTransition(role, from, to string) bool {
	if from == "DELETED" || from == to {
		return false
	}
	if role == "APPLICANT" {
		if to == "COMPLETED" {
			return from == "ASSIGNED" || from == "IN_PROGRESS" || from == "WAITING_FOR_APPLICANT" || from == "ANSWER_READY" || from == "RETURNED"
		}
		return to == "RETURNED" && from == "COMPLETED"
	}
	if to == "RETURNED" {
		return role == "OPERATOR" && (from == "ANSWER_READY" || from == "COMPLETED")
	}
	if to == "COMPLETED" {
		return role == "OPERATOR" && from == "ANSWER_READY"
	}
	if to == "CLOSED_NO_RESPONSE" || to == "REJECTED" {
		return role == "OPERATOR" && from != "COMPLETED" && from != "CLOSED_NO_RESPONSE" && from != "REJECTED"
	}
	if from == "COMPLETED" || from == "CLOSED_NO_RESPONSE" || from == "REJECTED" {
		return false
	}
	if role == "ADMIN" {
		return to == "ASSIGNED" || to == "IN_PROGRESS" || to == "WAITING_FOR_APPLICANT" || to == "ANSWER_READY" || to == "RETURNED"
	}
	if role == "EXPERT" || role == "OPERATOR" {
		switch to {
		case "IN_PROGRESS":
			return from == "ASSIGNED" || from == "RETURNED" || from == "WAITING_FOR_APPLICANT" || from == "ANSWER_READY"
		case "WAITING_FOR_APPLICANT", "ANSWER_READY":
			return from == "IN_PROGRESS" || from == "ASSIGNED" || from == "RETURNED" || from == "WAITING_FOR_APPLICANT"
		}
	}
	return false
}
func (s *Store) CaseAction(ctx context.Context, u Identity, id string, in Action) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize assignments and content actions so reassignment revokes access atomically.
	var old string
	err = tx.QueryRow(ctx, `SELECT status FROM appeals WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&old)
	if err != nil {
		return ErrNotFound
	}
	if err = authorizeCase(ctx, tx, u, id); err != nil {
		return err
	}
	newStatus := old
	switch in.Action {
	case "priority":
		if u.Role != "OPERATOR" && u.Role != "ADMIN" {
			return ErrForbidden
		}
		if u.Role == "ADMIN" && len(strings.TrimSpace(in.Body)) < 3 {
			return ErrInvalid
		}
		if in.Value != "LOW" && in.Value != "STANDARD" && in.Value != "URGENT" {
			return ErrInvalid
		}
		_, err = tx.Exec(ctx, `UPDATE appeals SET priority=$2,updated_at=now() WHERE id=$1`, id, in.Value)
	case "assign", "coexecutor":
		if u.Role != "OPERATOR" && u.Role != "ADMIN" {
			return ErrForbidden
		}
		if u.Role == "ADMIN" && len(strings.TrimSpace(in.Body)) < 3 {
			return ErrInvalid
		}
		if old == "COMPLETED" || old == "REJECTED" || old == "CLOSED_NO_RESPONSE" {
			return ErrInvalid
		}
		var valid bool
		var duplicate bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM appeal_assignments WHERE appeal_id=$1 AND expert_id=$2 AND active AND (kind='RESPONSIBLE' OR $3='coexecutor'))`, id, in.ExpertID, in.Action).Scan(&duplicate); err != nil {
			return ErrInvalid
		}
		if duplicate {
			return ErrInvalid
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_users WHERE id=$1 AND active AND role='EXPERT')`, in.ExpertID).Scan(&valid)
		if err != nil || !valid {
			return ErrInvalid
		}
		if in.RequestID != "" {
			var kind string
			err = tx.QueryRow(ctx, `SELECT kind FROM reassignment_requests WHERE id=$1 AND appeal_id=$2 AND status='NEW' FOR UPDATE`, in.RequestID, id).Scan(&kind)
			if err != nil {
				return ErrInvalid
			}
			if (kind == "COEXECUTOR") != (in.Action == "coexecutor") {
				return ErrInvalid
			}
		}
		kind := "RESPONSIBLE"
		if in.Action == "coexecutor" {
			kind = "COEXECUTOR"
		} else {
			_, err = tx.Exec(ctx, `UPDATE appeal_assignments SET active=false,ended_at=now() WHERE appeal_id=$1 AND active AND kind='RESPONSIBLE'`, id)
			if err != nil {
				return err
			}
			newStatus = "ASSIGNED"
			_, err = tx.Exec(ctx, `UPDATE appeals SET accepted_at=COALESCE(accepted_at,now()) WHERE id=$1`, id)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE appeal_assignments SET active=false,ended_at=now() WHERE appeal_id=$1 AND expert_id=$2 AND active`, id, in.ExpertID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO appeal_assignments(appeal_id,expert_id,kind,assigned_by) VALUES($1,$2,$3,$4)`, id, in.ExpertID, kind, u.ID)
		if err != nil {
			return err
		}
		if in.RequestID != "" {
			_, err = tx.Exec(ctx, `UPDATE reassignment_requests SET status='APPROVED',resolved_by=$2,resolved_at=now() WHERE id=$1`, in.RequestID, u.ID)
		}
	case "reject_request":
		if u.Role != "OPERATOR" && u.Role != "ADMIN" {
			return ErrForbidden
		}
		tag, e := tx.Exec(ctx, `UPDATE reassignment_requests SET status='REJECTED',resolved_by=$3,resolved_at=now() WHERE id=$1 AND appeal_id=$2 AND status='NEW'`, in.RequestID, id, u.ID)
		err = e
		if tag.RowsAffected() != 1 {
			return ErrInvalid
		}
	case "status":
		if !AllowedTransition(u.Role, old, in.Value) {
			return ErrForbidden
		}
		if u.Role == "ADMIN" && len(strings.TrimSpace(in.Body)) < 3 {
			return ErrInvalid
		}
		if in.Value == "REJECTED" && len(strings.TrimSpace(in.Body)) < 3 {
			return ErrInvalid
		}
		newStatus = in.Value
	case "operator_reply_close":
		if u.Role != "OPERATOR" || len(strings.TrimSpace(in.Body)) < 3 || old != "NEW" {
			return ErrForbidden
		}
		_, err = tx.Exec(ctx, `INSERT INTO appeal_messages(appeal_id,author_type,author_staff_id,body) VALUES($1,'SYSTEM',$2,$3)`, id, u.ID, in.Body)
		newStatus = "COMPLETED"
	case "reply", "note", "request":
		if u.Role != "EXPERT" {
			return ErrForbidden
		}
		if strings.TrimSpace(in.Body) == "" || len(in.Body) > 20000 {
			return ErrInvalid
		}
		if old == "COMPLETED" || old == "REJECTED" || old == "CLOSED_NO_RESPONSE" {
			return ErrInvalid
		}
		switch in.Action {
		case "reply":
			_, err = tx.Exec(ctx, `INSERT INTO appeal_messages(appeal_id,author_type,author_staff_id,body) VALUES($1,'EXPERT',$2,$3)`, id, u.ID, in.Body)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE appeals SET first_response_at=COALESCE(first_response_at,now()),updated_at=now() WHERE id=$1`, id)
			}
		case "note":
			_, err = tx.Exec(ctx, `INSERT INTO internal_notes(appeal_id,author_staff_id,body) VALUES($1,$2,$3)`, id, u.ID, in.Body)
		case "request":
			if in.Value != "TRANSFER" && in.Value != "COEXECUTOR" {
				return ErrInvalid
			}
			_, err = tx.Exec(ctx, `INSERT INTO reassignment_requests(appeal_id,requested_by,requester_id,kind,reason) VALUES($1,'EXPERT',$2,$3,$4)`, id, u.ID, in.Value, in.Body)
		}
	default:
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if newStatus != old {
		_, err = tx.Exec(ctx, `UPDATE appeals SET status=$2::appeal_status,updated_at=now(),resolved_at=CASE WHEN $2::text IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE') THEN now() ELSE resolved_at END WHERE id=$1`, id, newStatus)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO appeal_status_history(appeal_id,from_status,to_status,actor_staff_id,reason) VALUES($1,$2,$3,$4,NULLIF($5,''))`, id, old, newStatus, u.ID, strings.TrimSpace(in.Body))
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO staff_work_events(staff_id,appeal_id,action) VALUES($1,$2,$3)`, u.ID, id, in.Action)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_staff_id,action,resource_type,resource_id,reason) VALUES($1,$2,'appeal',$3,NULLIF($4,''))`, u.ID, in.Action, id, strings.TrimSpace(in.Body))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES('appeal.status_changed',$1,'{}')`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ApplicantStatus(ctx context.Context, id, to string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	err = tx.QueryRow(ctx, `SELECT status FROM appeals WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&old)
	if err != nil {
		return ErrNotFound
	}
	if !AllowedTransition("APPLICANT", old, to) {
		return ErrForbidden
	}
	_, err = tx.Exec(ctx, `UPDATE appeals SET status=$2::appeal_status,updated_at=now(),resolved_at=CASE WHEN $2::text='COMPLETED' THEN now() ELSE resolved_at END WHERE id=$1`, id, to)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO appeal_status_history(appeal_id,from_status,to_status) VALUES($1,$2,$3)`, id, old, to)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES('appeal.status_changed',$1,'{}')`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ApplicantResolve(ctx context.Context, id string, helpful bool, reason string, rating int, comment string, complaint bool, complaintText string) error {
	if rating < 0 || rating > 5 || len(comment) > 2000 || len(complaintText) > 2000 || (!helpful && len(strings.TrimSpace(reason)) < 3) || (complaint && len(strings.TrimSpace(complaintText)) < 3) {
		return ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	var returns int
	if err = tx.QueryRow(ctx, `SELECT status,return_count FROM appeals WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&old, &returns); err != nil {
		return ErrNotFound
	}
	if old != "ANSWER_READY" && old != "COMPLETED" && !complaint {
		return ErrForbidden
	}
	if old == "ANSWER_READY" {
		to := "COMPLETED"
		if !helpful {
			if returns >= 2 {
				return ErrInvalid
			}
			to = "RETURNED"
			_, err = tx.Exec(ctx, `UPDATE appeal_assignments SET active=false,ended_at=now() WHERE appeal_id=$1 AND active`, id)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE appeals SET status=$2::appeal_status,return_count=return_count+CASE WHEN $2::text='RETURNED' THEN 1 ELSE 0 END,updated_at=now(),resolved_at=CASE WHEN $2::text='COMPLETED' THEN now() ELSE resolved_at END WHERE id=$1`, id, to)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO appeal_status_history(appeal_id,from_status,to_status,reason) VALUES($1,$2,$3,NULLIF($4,''))`, id, old, to, strings.TrimSpace(reason))
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO appeal_feedback(appeal_id,helpful,rating,comment,complaint,complaint_text) VALUES($1,$2,NULLIF($3,0),NULLIF($4,''),$5,NULLIF($6,'')) ON CONFLICT(appeal_id) DO UPDATE SET helpful=COALESCE(EXCLUDED.helpful,appeal_feedback.helpful),rating=COALESCE(EXCLUDED.rating,appeal_feedback.rating),comment=COALESCE(EXCLUDED.comment,appeal_feedback.comment),complaint=EXCLUDED.complaint OR appeal_feedback.complaint,complaint_text=COALESCE(EXCLUDED.complaint_text,appeal_feedback.complaint_text),updated_at=now()`, id, helpful, rating, strings.TrimSpace(comment), complaint, strings.TrimSpace(complaintText))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_type,aggregate_id,payload) VALUES('appeal.status_changed',$1,'{}')`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) StaffAttachment(ctx context.Context, u Identity, appealID, attachmentID string) (string, string, []byte, error) {
	if u.Role == "ADMIN" {
		return "", "", nil, ErrForbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", nil, err
	}
	defer tx.Rollback(ctx)
	if err = authorizeCase(ctx, tx, u, appealID); err != nil {
		return "", "", nil, err
	}
	var name, contentType string
	var data []byte
	err = tx.QueryRow(ctx, `SELECT f.file_name,f.content_type,f.content FROM appeal_attachments f JOIN appeals a ON a.id=f.appeal_id WHERE f.id=$1 AND f.appeal_id=$2 AND (f.message_id IS NULL OR $3='EXPERT' OR ($3='OPERATOR' AND a.status IN ('NEW','RETURNED') AND NOT EXISTS(SELECT 1 FROM appeal_assignments aa WHERE aa.appeal_id=a.id AND aa.active AND aa.kind='RESPONSIBLE')))`, attachmentID, appealID, u.Role).Scan(&name, &contentType, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, ErrNotFound
	}
	return name, contentType, data, err
}
