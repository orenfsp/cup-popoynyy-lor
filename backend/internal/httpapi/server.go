package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/hackathon/otklik/backend/internal/config"
	"github.com/hackathon/otklik/backend/internal/store"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	cfg   config.Config
	store *store.Store
	redis *redis.Client
	log   *slog.Logger
	mux   *http.ServeMux
}

func New(cfg config.Config, st *store.Store, rdb *redis.Client, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, store: st, redis: rdb, log: log, mux: http.NewServeMux()}
	s.routes()
	s.workflowRoutes()
	return s.security(s.recover(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.health)
	s.mux.HandleFunc("GET /api/v1/config/public", s.publicConfig)
	s.mux.HandleFunc("GET /api/v1/questionnaire", s.questionnaire)
	s.mux.HandleFunc("POST /api/v1/appeals", s.createAppeal)
	s.mux.HandleFunc("POST /api/v1/appeal-sessions", s.createAppealSession)
	s.mux.HandleFunc("GET /api/v1/my-appeal", s.withApplicant(s.getMyAppeal))
	s.mux.HandleFunc("POST /api/v1/my-appeal/messages", s.withApplicant(s.addApplicantMessage))
	s.mux.HandleFunc("POST /api/v1/my-appeal/feedback", s.withApplicant(s.applicantFeedback))
	s.mux.HandleFunc("GET /api/v1/my-appeal/attachments/{id}", s.withApplicant(s.applicantAttachment))
	s.mux.HandleFunc("DELETE /api/v1/my-appeal", s.withApplicant(s.deleteAppeal))
	s.mux.HandleFunc("POST /api/v1/my-appeal/reassignment-requests", s.withApplicant(s.requestReassignment))
	s.mux.HandleFunc("GET /api/v1/admin/config", s.requireAdmin(s.adminConfig))
	// Aggregate reports are admin-only; employee-scoped statistics use /staff/statistics.
	s.mux.HandleFunc("GET /api/v1/analytics", s.requireAdmin(s.analytics))
	s.mux.HandleFunc("GET /api/v1/admin/staff", s.requireAdmin(s.listStaff))
	s.mux.HandleFunc("POST /api/v1/admin/staff", s.requireAdmin(s.createStaff))
	s.mux.HandleFunc("GET /api/v1/admin/questions", s.requireAdmin(s.adminQuestions))
	s.mux.HandleFunc("POST /api/v1/admin/questions", s.requireAdmin(s.createQuestion))
	s.mux.HandleFunc("PUT /api/v1/admin/questions/{id}/active", s.requireAdmin(s.toggleQuestion))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Сервис временно недоступен")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"feedbackEmailAvailable": true,
	})
}
func (s *Server) questionnaire(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Questions(r.Context(), true)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) createAppeal(w http.ResponseWriter, r *http.Request) {
	in, attachments, ok := decodeAppealRequest(w, r)
	if !ok {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if len(in.Body) < 10 || len(in.Body) > 20000 {
		writeError(w, 422, "invalid_body", "Можешь добавить немного деталей? Так будет проще помочь")
		return
	}
	if in.ApplicantType != "STUDENT" && in.ApplicantType != "PARENT" && in.ApplicantType != "TEACHER" {
		writeError(w, 422, "invalid_applicant_type", "Выбери, от чьего лица создаётся обращение")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if in.Email != "" {
		address, parseErr := mail.ParseAddress(in.Email)
		if parseErr != nil || !strings.EqualFold(address.Address, in.Email) || len(in.Email) > 320 {
			writeError(w, 422, "invalid_email", "Проверьте адрес электронной почты")
			return
		}
	}
	publicID, secret, err := credentials()
	if err != nil {
		s.internal(w, err)
		return
	}
	crisis := detectCrisis(in.Body + " " + answersText(in.Answers))
	a, err := s.store.CreateAppeal(r.Context(), store.CreateAppealInput{
		ApplicantType: in.ApplicantType, CategoryCode: in.CategoryCode, Body: in.Body,
		Answers: in.Answers, Email: in.Email, CrisisFlag: crisis, Attachments: attachments,
	}, publicID, secret)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"appeal": a, "publicId": publicID, "accessCode": secret,
		"accessFragment":     "/access#" + secret,
		"crisisHelpRequired": crisis,
	})
}

func (s *Server) applicantFeedback(w http.ResponseWriter, r *http.Request, appealID string) {
	var in struct {
		Helpful       bool   `json:"helpful"`
		Reason        string `json:"reason"`
		Rating        int    `json:"rating"`
		Comment       string `json:"comment"`
		Complaint     bool   `json:"complaint"`
		ComplaintText string `json:"complaintText"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.ApplicantResolve(r.Context(), appealID, in.Helpful, in.Reason, in.Rating, in.Comment, in.Complaint, in.ComplaintText); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) applicantAttachment(w http.ResponseWriter, r *http.Request, appealID string) {
	name, contentType, data, err := s.store.ApplicantAttachment(r.Context(), appealID, r.PathValue("id"))
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Write(data)
}

func (s *Server) createAppealSession(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r, "access", 5, time.Minute) {
		writeError(w, 429, "too_many_attempts", "Попробуй ещё раз немного позже")
		return
	}
	var in struct {
		AccessCode string `json:"accessCode"`
	}
	if !decode(w, r, &in) {
		return
	}
	appealID, err := s.store.AuthenticateAppeal(r.Context(), strings.ToUpper(strings.TrimSpace(in.AccessCode)))
	if err != nil {
		time.Sleep(250 * time.Millisecond)
		writeError(w, 401, "invalid_access", "Не получилось открыть обращение. Проверь номер и секрет")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err := s.store.CreateAnonymousSession(r.Context(), appealID, s.store.HashSecret(token), time.Now().Add(s.cfg.SessionTTL)); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "otklik_appeal", Value: token, Path: "/api/v1/my-appeal", HttpOnly: true, Secure: s.cfg.Environment == "production", SameSite: http.SameSiteStrictMode, MaxAge: int(s.cfg.SessionTTL.Seconds())})
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) getMyAppeal(w http.ResponseWriter, r *http.Request, appealID string) {
	a, messages, err := s.store.GetAppeal(r.Context(), appealID)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"appeal": a, "messages": messages})
}

func (s *Server) addApplicantMessage(w http.ResponseWriter, r *http.Request, appealID string) {
	in, attachments, ok := decodeChatMessageRequest(w, r)
	if !ok {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" && len(attachments) == 0 {
		writeError(w, 422, "invalid_message", "Напиши сообщение или приложи скриншот")
		return
	}
	if len(in.Body) > 20000 {
		writeError(w, 422, "invalid_message", "Сообщение получилось слишком длинным")
		return
	}
	if in.Body == "" {
		in.Body = "Прикреплены файлы"
	}
	crisis := detectCrisis(in.Body)
	m, err := s.store.AddApplicantMessage(r.Context(), appealID, in.Body, attachments, crisis)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"message": m, "crisisHelpRequired": crisis})
}

func (s *Server) deleteAppeal(w http.ResponseWriter, r *http.Request, appealID string) {
	if err := s.store.DeleteAppeal(r.Context(), appealID); err != nil {
		s.internal(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "otklik_appeal", Value: "", Path: "/api/v1/my-appeal", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) requestReassignment(w http.ResponseWriter, r *http.Request, appealID string) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.RequestReassignment(r.Context(), appealID, in.Reason); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, map[string]bool{"ok": true})
}

func (s *Server) adminConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"analyticsEnabled": s.cfg.AnalyticsEnabled, "kAnonymity": s.cfg.KAnonymity})
}

func (s *Server) analytics(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AnalyticsEnabled {
		writeError(w, 404, "feature_disabled", "Аналитика отключена при запуске сервиса")
		return
	}
	rows, err := s.store.Analytics(r.Context(), s.cfg.KAnonymity)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "kAnonymity": s.cfg.KAnonymity})
}

func (s *Server) listStaff(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListStaff(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createStaff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 12 {
		s.internal(w, store.ErrInvalid)
		return
	}
	u, err := s.store.CreateStaff(r.Context(), in.Email, in.DisplayName, in.Role)
	if err != nil {
		s.internal(w, err)
		return
	}
	if err = s.store.SetStaffPassword(r.Context(), u.ID, in.Password); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, u)
}
func (s *Server) adminQuestions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Questions(r.Context(), false)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) createQuestion(w http.ResponseWriter, r *http.Request) {
	var q store.Question
	if !decode(w, r, &q) {
		return
	}
	if !validQuestion(q) {
		s.internal(w, store.ErrInvalid)
		return
	}
	created, err := s.store.CreateQuestion(r.Context(), q)
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 201, created)
}
func (s *Server) toggleQuestion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Active bool `json:"active"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.ToggleQuestion(r.Context(), r.PathValue("id"), in.Active); err != nil {
		s.internal(w, err)
		return
	}
	w.WriteHeader(204)
}

type applicantHandler func(http.ResponseWriter, *http.Request, string)

func (s *Server) withApplicant(next applicantHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("otklik_appeal")
		if err != nil {
			writeError(w, 401, "authentication_required", "Открой обращение с помощью сохранённого кода")
			return
		}
		appealID, err := s.store.AppealIDBySession(r.Context(), cookie.Value)
		if err != nil {
			writeError(w, 401, "authentication_required", "Сессия завершена. Введи код доступа ещё раз")
			return
		}
		next(w, r, appealID)
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.staffOnly([]string{"ADMIN"}, next)
}

func (s *Server) allow(r *http.Request, action string, limit int64, window time.Duration) bool {
	key := "rl:" + action + ":" + clientPrefix(r)
	n, err := s.redis.Incr(r.Context(), key).Result()
	if err != nil {
		return true
	}
	if n == 1 {
		s.redis.Expire(r.Context(), key, window)
	}
	return n <= limit
}

func clientPrefix(r *http.Request) string {
	value := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		value = strings.Split(forwarded, ",")[0]
	}
	return value
}

func credentials() (string, string, error) {
	const alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	makePart := func(n int) (string, error) {
		b := make([]byte, n)
		raw := make([]byte, n)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = alphabet[int(raw[i])%len(alphabet)]
		}
		return string(b), nil
	}
	a, err := makePart(8)
	if err != nil {
		return "", "", err
	}
	b, err := makePart(32)
	if err != nil {
		return "", "", err
	}
	return "МОЛ-" + a[:4] + "-" + a[4:], "МОЛ-" + b[:4] + "-" + b[4:8] + "-" + b[8:12] + "-" + b[12:16] + "-" + b[16:20] + "-" + b[20:24] + "-" + b[24:28] + "-" + b[28:], nil
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func detectCrisis(body string) bool {
	v := strings.ToLower(body)
	for _, word := range []string{
		"суицид", "самоуб", "покончить с собой", "убить себя", "не хочу жить",
		"хочу умереть", "умереть", "лучше умер", "смерти", "повеситься", "выпрыгнуть",
		"порезать себя", "навредить себе", "угрожает убить", "угрожают убить",
		"физическое насилие", "меня бьют", "избивают", "изнасил",
	} {
		if strings.Contains(v, word) {
			return true
		}
	}
	return false
}

func answersText(answers map[string]any) string {
	raw, _ := json.Marshal(answers)
	return string(raw)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_json", "Не получилось прочитать данные")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrForbidden) {
		writeError(w, 403, "forbidden", "Действие недоступно")
		return
	}
	if errors.Is(err, store.ErrInvalid) {
		writeError(w, 422, "invalid", "Проверьте поля и текущий статус обращения")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "not_found", "Не найдено")
		return
	}
	s.log.Error("request failed", "error", err)
	writeError(w, 500, "internal_error", "Что-то пошло не так. Попробуй ещё раз")
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "value", v)
				writeError(w, 500, "internal_error", "Что-то пошло не так")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Origin", s.cfg.FrontendOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-Molva-Request")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", s.cfg.FrontendOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		next.ServeHTTP(w, r)
	})
}
