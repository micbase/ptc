-- Migration: switch_events table to record when the user switched electricity plans
CREATE TABLE IF NOT EXISTS switch_events (
    id                       SERIAL PRIMARY KEY,
    electricity_rate_id      INTEGER NOT NULL REFERENCES electricity_rates(id),
    switch_date              DATE NOT NULL,
    contract_expiration_date DATE NOT NULL,
    notes                    TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_switch_events_switch_date ON switch_events(switch_date);
CREATE INDEX IF NOT EXISTS idx_switch_events_electricity_rate_id ON switch_events(electricity_rate_id);
