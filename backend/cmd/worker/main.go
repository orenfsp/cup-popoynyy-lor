package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hackathon/otklik/backend/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type event struct {
	ID, Type, AggregateID string
	Payload               []byte
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	log.Info("worker started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := runBatch(ctx, pool, cfg); err != nil {
				log.Error("outbox batch failed", "error", err)
			}
		}
	}
}

func runBatch(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) error {
	if err := closeInactiveAppeals(ctx, pool, cfg.AutoCloseDays); err != nil {
		return err
	}
	for i := 0; i < 50; i++ {
		handled, err := processOne(ctx, pool, cfg)
		if err != nil {
			return err
		}
		if !handled {
			return nil
		}
	}
	return nil
}

func closeInactiveAppeals(ctx context.Context, pool *pgxpool.Pool, days int) error {
	_, err := pool.Exec(ctx, `WITH closed AS (
		UPDATE appeals SET status='CLOSED_NO_RESPONSE',resolved_at=now(),updated_at=now()
		WHERE status='WAITING_FOR_APPLICANT' AND updated_at < now()-($1::text||' days')::interval
		RETURNING id
	), history AS (
		INSERT INTO appeal_status_history(appeal_id,from_status,to_status,reason)
		SELECT id,'WAITING_FOR_APPLICANT','CLOSED_NO_RESPONSE','Нет ответа заявителя' FROM closed
	)
	INSERT INTO outbox_events(event_type,aggregate_id,payload)
	SELECT 'appeal.status_changed',id,'{}'::jsonb FROM closed`, days)
	return err
}

func processOne(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) (bool, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var e event
	err = tx.QueryRow(ctx, `SELECT id,event_type,aggregate_id,payload FROM outbox_events WHERE processed_at IS NULL ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&e.ID, &e.Type, &e.AggregateID, &e.Payload)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = handle(ctx, tx, cfg, e); err != nil {
		_, _ = tx.Exec(ctx, `UPDATE outbox_events SET attempts=attempts+1 WHERE id=$1`, e.ID)
		return true, err
	}
	_, err = tx.Exec(ctx, `UPDATE outbox_events SET processed_at=now(),attempts=attempts+1 WHERE id=$1`, e.ID)
	if err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func handle(ctx context.Context, tx pgx.Tx, cfg config.Config, e event) error {
	switch e.Type {
	case "appeal.created", "appeal.status_changed":
		return refreshFacts(ctx, tx, cfg, e.AggregateID)
	case "appeal.deletion_requested":
		if err := refreshFacts(ctx, tx, cfg, e.AggregateID); err != nil {
			return err
		}
		for _, query := range []string{`DELETE FROM appeal_messages WHERE appeal_id=$1`, `DELETE FROM internal_notes WHERE appeal_id=$1`, `DELETE FROM appeal_contents WHERE appeal_id=$1`} {
			if _, err := tx.Exec(ctx, query, e.AggregateID); err != nil {
				return err
			}
		}
		return nil
	default:
		var discard map[string]any
		return json.Unmarshal(e.Payload, &discard)
	}
}

func refreshFacts(ctx context.Context, tx pgx.Tx, cfg config.Config, appealID string) error {
	if !cfg.AnalyticsEnabled {
		return nil
	}
	// Keep the historical HMAC namespace so existing anonymized facts retain their identifiers.
	mac := hmac.New(sha256.New, []byte(cfg.TokenPepper+":closed-analytics"))
	mac.Write([]byte(appealID))
	analyticsID := mac.Sum(nil)
	_, err := tx.Exec(ctx, `
		INSERT INTO analytics_facts(analytics_id,event_day,category_code,applicant_type,final_status,priority,crisis_flag,returned,acceptance_seconds,first_response_seconds,resolution_seconds)
		SELECT $2,a.created_at::date,c.code,a.applicant_type,a.status,a.priority,a.crisis_flag,
		       EXISTS(SELECT 1 FROM appeal_status_history h WHERE h.appeal_id=a.id AND h.to_status='RETURNED'),
		       EXTRACT(EPOCH FROM(a.accepted_at-a.created_at))::bigint,
		       EXTRACT(EPOCH FROM(a.first_response_at-a.created_at))::bigint,
		       EXTRACT(EPOCH FROM(a.resolved_at-a.created_at))::bigint
		FROM appeals a JOIN categories c ON c.id=a.category_id WHERE a.id=$1
		ON CONFLICT(analytics_id) DO UPDATE SET final_status=EXCLUDED.final_status,priority=EXCLUDED.priority,crisis_flag=EXCLUDED.crisis_flag,returned=EXCLUDED.returned,acceptance_seconds=EXCLUDED.acceptance_seconds,first_response_seconds=EXCLUDED.first_response_seconds,resolution_seconds=EXCLUDED.resolution_seconds`, appealID, analyticsID)
	return err
}
