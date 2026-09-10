DO $$
BEGIN
  IF to_regclass('public.analytics_facts') IS NULL
     AND to_regclass('public.analytics_closed_facts') IS NOT NULL THEN
    ALTER TABLE analytics_closed_facts RENAME TO analytics_facts;
  END IF;
END $$;

DROP TABLE IF EXISTS analytics_open_facts;
DROP TABLE IF EXISTS analytics_consents;
DROP TYPE IF EXISTS analytics_scope;
