-- Migration: ensure unique index on (id_key, fetch_date)
-- Safe to run on existing databases that were created before the UNIQUE constraint was in the schema.
CREATE UNIQUE INDEX IF NOT EXISTS idx_electricity_rates_id_key_fetch_date
    ON electricity_rates (id_key, fetch_date);
