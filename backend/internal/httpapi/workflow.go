package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/hackathon/otklik/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"os"
	"strings"
	"time"
)

type identityKey struct{}

func identity(r *http.Request) store.Identity {
	return r.Context().Value(identityKey{}).(store.Identity)
}
func (s *Server) staffOnly(roles []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("molva_staff")
		if err != nil {
			writeError(w, 401, "unauthorized", "Войдите в кабинет")
			return
		}
		u, err := s.store.StaffSession(r.Context(), c.Value)
		if err != nil {
			writeError(w, 401, "unauthorized", "Сессия завершена")
			return
		}
		allowed := false
		for _, role := range roles {
			if role == u.Role {
				allowed = true
			}
		}
		if !allowed {
			writeError(w, 403, "forbidden", "Недостаточно прав")
			return
		}
		if r.Method != "GET" && r.Header.Get("X-Molva-Request") != "1" {
			writeError(w, 403, "forbidden", "Неверный запрос")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, u)))
	}
}
func (s *Server) workflowRoutes() {
	if password := os.Getenv("STAFF_BOOTSTRAP_PASSWORD"); password != "" {
		if err := s.store.BootstrapStaff(context.Background(), password); err != nil {
			s.log.Error("staff bootstrap failed", "error", err)
		}
	}
	s.mux.HandleFunc("POST /api/v1/staff/login", s.staffLogin)
	all := []string{"ADMIN", "OPERATOR", "EXPERT"}
	s.mux.HandleFunc("GET /api/v1/staff/me", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, identity(r)) }))
	s.mux.HandleFunc("POST /api/v1/staff/logout", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie("molva_staff")
		_, err := s.store.Pool.Exec(r.Context(), `DELETE FROM staff_sessions WHERE token_hash=$1`, s.store.HashSecret(c.Value))
		if err != nil {
			s.internal(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "molva_staff", Path: "/api/v1", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("GET /api/v1/staff/cases", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.store.Cases(r.Context(), identity(r))
		if err != nil {
			s.internal(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
	}))
	s.mux.HandleFunc("GET /api/v1/staff/cases/{id}", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		d, err := s.store.CaseDetail(r.Context(), identity(r), r.PathValue("id"))
		if err != nil {
			s.internal(w, err)
			return
		}
		writeJSON(w, 200, d)
	}))
	s.mux.HandleFunc("GET /api/v1/staff/cases/{id}/attachments/{attachmentId}", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		name, contentType, data, err := s.store.StaffAttachment(r.Context(), identity(r), r.PathValue("id"), r.PathValue("attachmentId"))
		if err != nil {
			s.internal(w, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
		w.Write(data)
	}))
	s.mux.HandleFunc("POST /api/v1/staff/cases/{id}/actions", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		var in store.Action
		if !decode(w, r, &in) {
			return
		}
		if err := s.store.CaseAction(r.Context(), identity(r), r.PathValue("id"), in); err != nil {
			s.internal(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("POST /api/v1/staff/cases/{id}/messages", s.staffOnly([]string{"OPERATOR", "EXPERT"}, func(w http.ResponseWriter, r *http.Request) {
		in, attachments, ok := decodeChatMessageRequest(w, r)
		if !ok {
			return
		}
		in.Body = strings.TrimSpace(in.Body)
		if in.Body == "" && len(attachments) == 0 {
			writeError(w, 422, "invalid_message", "Напишите сообщение или приложите скриншот")
			return
		}
		if len(in.Body) > 20000 {
			writeError(w, 422, "invalid_message", "Сообщение получилось слишком длинным")
			return
		}
		if in.Body == "" {
			in.Body = "Прикреплены файлы"
		}
		if err := s.store.AddStaffMessage(r.Context(), identity(r), r.PathValue("id"), in.Body, attachments); err != nil {
			s.internal(w, err)
			return
		}
		w.WriteHeader(201)
	}))
	s.mux.HandleFunc("GET /api/v1/staff/experts", s.staffOnly(all, func(w http.ResponseWriter, r *http.Request) {
		items, err := s.store.Maps(r.Context(), `SELECT u.id,u.display_name AS name,count(a.id) FILTER (WHERE ap.status NOT IN ('COMPLETED','REJECTED','CLOSED_NO_RESPONSE','DELETED')) AS active_count FROM staff_users u LEFT JOIN appeal_assignments a ON a.expert_id=u.id AND a.active LEFT JOIN appeals ap ON ap.id=a.appeal_id WHERE u.active AND u.role='EXPERT' GROUP BY u.id,u.display_name ORDER BY u.display_name`)
		if err != nil {
			s.internal(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	}))
	s.mux.HandleFunc("GET /api/v1/staff/statistics", s.staffOnly(all, s.staffStatistics))
	s.mux.HandleFunc("POST /api/v1/my-appeal/status", s.withApplicant(func(w http.ResponseWriter, r *http.Request, id string) {
		var in struct {
			Status string `json:"status"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.store.ApplicantStatus(r.Context(), id, in.Status); err != nil {
			s.internal(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	s.mux.HandleFunc("GET /api/v1/categories", s.categories)
	s.mux.HandleFunc("GET /api/v1/admin/categories", s.requireAdmin(s.categories))
	s.mux.HandleFunc("POST /api/v1/admin/categories", s.requireAdmin(s.saveCategory))
	s.mux.HandleFunc("GET /api/v1/admin/groups", s.requireAdmin(s.groups))
	s.mux.HandleFunc("POST /api/v1/admin/groups", s.requireAdmin(s.saveGroup))
	s.mux.HandleFunc("GET /api/v1/admin/audit", s.requireAdmin(s.auditLog))
	s.mux.HandleFunc("PUT /api/v1/admin/staff/{id}", s.requireAdmin(s.saveStaff))
	s.mux.HandleFunc("PUT /api/v1/admin/questions/{id}", s.requireAdmin(s.saveQuestion))
}
func (s *Server) staffLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r, "staff-login", 10, time.Minute) {
		writeError(w, 429, "rate_limit", "Повторите через минуту")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, err := randomToken(32)
	if err != nil {
		s.internal(w, err)
		return
	}
	u, err := s.store.Login(r.Context(), in.Email, in.Password, token)
	if err != nil {
		writeError(w, 401, "unauthorized", "Неверная почта или пароль")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "molva_staff", Value: token, Path: "/api/v1", HttpOnly: true, Secure: s.cfg.Environment == "production", SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	writeJSON(w, 200, u)
}
func (s *Server) staffStatistics(w http.ResponseWriter, r *http.Request) {
	u := identity(r)
	if !s.cfg.AnalyticsEnabled {
		writeError(w, 404, "disabled", "Аналитика отключена")
		return
	}
	var rows any
	if u.Role == "ADMIN" {
		items, err := s.store.Analytics(r.Context(), s.cfg.KAnonymity)
		if err != nil {
			s.internal(w, err)
			return
		}
		rows = items
	} else {
		query := `SELECT date_trunc('day',e.created_at) AS day,e.action,count(*) AS count FROM staff_work_events e WHERE e.staff_id=$1`
		query += ` GROUP BY 1,2 ORDER BY 1 DESC,2`
		items, err := s.store.Maps(r.Context(), query, u.ID)
		if err != nil {
			s.internal(w, err)
			return
		}
		rows = items
	}
	table := statisticsTable(rows)
	if r.URL.Query().Get("format") == "xlsx" {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="molva-statistics.xlsx"`)
		if err := writeXLSX(w, table); err != nil {
			s.log.Error("xlsx export failed", "error", err)
		}
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="molva-statistics.csv"`)
		writer := csv.NewWriter(w)
		for _, row := range table {
			_ = writer.Write(row)
		}
		writer.Flush()
		return
	}
	response := map[string]any{"items": rows, "personal": u.Role != "ADMIN"}
	if u.Role == "ADMIN" {
		overview, err := s.store.AnalyticsOverview(r.Context(), s.cfg.KAnonymity)
		if err != nil {
			s.internal(w, err)
			return
		}
		response["overview"] = overview
	}
	writeJSON(w, 200, response)
}

func statisticsTable(rows any) [][]string {
	table := make([][]string, 0)
	switch items := rows.(type) {
	case []store.AnalyticsRow:
		table = append(table, []string{"Дата", "Категория", "Обращений", "Срочных", "Возвратов", "Удалено", "Среднее до принятия, сек", "Среднее до ответа, сек", "Среднее до закрытия, сек"})
		for _, v := range items {
			table = append(table, []string{v.Day.Format("2006-01-02"), safeCSV(v.Category), fmt.Sprint(v.Count), fmt.Sprint(v.UrgentCount), fmt.Sprint(v.ReturnedCount), fmt.Sprint(v.DeletedCount), fmt.Sprint(v.AverageAcceptanceSeconds), fmt.Sprint(v.AverageFirstResponseSeconds), fmt.Sprint(v.AverageResolutionSeconds)})
		}
	case []map[string]any:
		table = append(table, []string{"Дата", "Действие", "Количество"})
		for _, v := range items {
			table = append(table, []string{fmt.Sprint(v["day"]), safeCSV(fmt.Sprint(v["action"])), fmt.Sprint(v["count"])})
		}
	}
	return table
}
func safeCSV(v string) string {
	if strings.HasPrefix(v, "=") || strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "@") {
		return "'" + v
	}
	return v
}
func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	q := `SELECT id,code,title FROM categories WHERE active ORDER BY sort_order,title`
	if strings.Contains(r.URL.Path, "/admin/") {
		q = `SELECT c.id,c.code,c.title,c.active,c.default_expert_id,rr.group_id,g.name AS group_name FROM categories c LEFT JOIN category_routing_rules rr ON rr.category_id=c.id LEFT JOIN specialist_groups g ON g.id=rr.group_id ORDER BY c.sort_order,c.title`
	}
	items, err := s.store.Maps(r.Context(), q)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) saveCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code     string `json:"code"`
		Title    string `json:"title"`
		Active   bool   `json:"active"`
		ExpertID string `json:"expertId"`
		GroupID  string `json:"groupId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Title) == "" {
		s.internal(w, store.ErrInvalid)
		return
	}
	if in.ExpertID != "" {
		var valid bool
		err := s.store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM staff_users WHERE id=$1 AND active AND role='EXPERT')`, in.ExpertID).Scan(&valid)
		if err != nil || !valid {
			s.internal(w, store.ErrInvalid)
			return
		}
	}
	tx, err := s.store.Pool.Begin(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var categoryID string
	err = tx.QueryRow(r.Context(), `INSERT INTO categories(code,title,active,default_expert_id) VALUES($1,$2,$3,NULLIF($4,'')::uuid) ON CONFLICT(code) DO UPDATE SET title=excluded.title,active=excluded.active,default_expert_id=excluded.default_expert_id RETURNING id`, in.Code, in.Title, in.Active, in.ExpertID).Scan(&categoryID)
	if err == nil {
		if in.GroupID == "" {
			_, err = tx.Exec(r.Context(), `DELETE FROM category_routing_rules WHERE category_id=$1`, categoryID)
		} else {
			_, err = tx.Exec(r.Context(), `INSERT INTO category_routing_rules(category_id,group_id) SELECT $1,id FROM specialist_groups WHERE id=$2 AND active ON CONFLICT(category_id) DO UPDATE SET group_id=excluded.group_id`, categoryID, in.GroupID)
		}
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Maps(r.Context(), `SELECT g.id,g.name,g.max_active_per_expert,g.active,COALESCE(jsonb_agg(gm.expert_id) FILTER (WHERE gm.expert_id IS NOT NULL),'[]') AS expert_ids FROM specialist_groups g LEFT JOIN specialist_group_members gm ON gm.group_id=g.id GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		MaxActive int      `json:"maxActive"`
		Active    bool     `json:"active"`
		ExpertIDs []string `json:"expertIds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.MaxActive < 1 || in.MaxActive > 500 {
		s.internal(w, store.ErrInvalid)
		return
	}
	tx, err := s.store.Pool.Begin(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	groupID := in.ID
	if groupID == "" {
		err = tx.QueryRow(r.Context(), `INSERT INTO specialist_groups(name,max_active_per_expert,active) VALUES($1,$2,$3) RETURNING id`, strings.TrimSpace(in.Name), in.MaxActive, in.Active).Scan(&groupID)
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE specialist_groups SET name=$2,max_active_per_expert=$3,active=$4 WHERE id=$1`, groupID, strings.TrimSpace(in.Name), in.MaxActive, in.Active)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM specialist_group_members WHERE group_id=$1`, groupID)
	}
	for _, expertID := range in.ExpertIDs {
		if err != nil {
			break
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO specialist_group_members(group_id,expert_id) SELECT $1,id FROM staff_users WHERE id=$2 AND active AND role='EXPERT'`, groupID, expertID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_staff_id,action,resource_type,resource_id) VALUES($1,'routing_group_saved','specialist_group',$2)`, identity(r).ID, groupID)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Maps(r.Context(), `SELECT e.id,e.created_at,COALESCE(u.display_name,'Система') AS actor,e.action,e.resource_type,e.resource_id,COALESCE(e.reason,'') AS reason FROM audit_events e LEFT JOIN staff_users u ON u.id=e.actor_staff_id ORDER BY e.created_at DESC LIMIT 300`)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) saveStaff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
		Active      bool   `json:"active"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != "ADMIN" && in.Role != "OPERATOR" && in.Role != "EXPERT" {
		s.internal(w, store.ErrInvalid)
		return
	}
	if r.PathValue("id") == identity(r).ID && (!in.Active || in.Role != "ADMIN") {
		writeError(w, 422, "invalid", "Нельзя отключить собственную учётную запись администратора")
		return
	}
	hash := ""
	if in.Password != "" {
		if len(in.Password) < 12 {
			s.internal(w, store.ErrInvalid)
			return
		}
		b, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			s.internal(w, err)
			return
		}
		hash = string(b)
	}
	tx, err := s.store.Pool.Begin(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE staff_users SET display_name=$2,email=$3,role=$4,active=$5,password_hash=CASE WHEN $6='' THEN password_hash ELSE $6 END WHERE id=$1`, r.PathValue("id"), in.DisplayName, in.Email, in.Role, in.Active, hash)
	if err == nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM staff_sessions WHERE staff_id=$1`, r.PathValue("id"))
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) saveQuestion(w http.ResponseWriter, r *http.Request) {
	var q store.Question
	if !decode(w, r, &q) {
		return
	}
	if !validQuestion(q) {
		s.internal(w, store.ErrInvalid)
		return
	}
	if err := s.store.ValidateQuestionCondition(r.Context(), q.Code, q.ShowIfQuestionCode, q.ShowIfValues, q.SortOrder, r.PathValue("id")); err != nil {
		s.internal(w, err)
		return
	}
	options, _ := json.Marshal(q.Options)
	conditions, _ := json.Marshal(q.ShowIfValues)
	_, err := s.store.Pool.Exec(r.Context(), `UPDATE questionnaire_questions SET title_student=$2,title_adult=$3,answer_type=$4,options=$5,required=$6,active=$7,sort_order=$8,show_if_question_code=NULLIF($9,''),show_if_values=$10,updated_at=now() WHERE id=$1`, r.PathValue("id"), q.TitleStudent, q.TitleAdult, q.AnswerType, options, q.Required, q.Active, q.SortOrder, q.ShowIfQuestionCode, conditions)
	if err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}
func validQuestion(q store.Question) bool {
	return strings.TrimSpace(q.Code) != "" && strings.TrimSpace(q.TitleStudent) != "" && strings.TrimSpace(q.TitleAdult) != "" && (q.AnswerType == "TEXT" || ((q.AnswerType == "SINGLE" || q.AnswerType == "MULTIPLE") && len(q.Options) > 0)) && ((q.ShowIfQuestionCode == "") == (len(q.ShowIfValues) == 0))
}
