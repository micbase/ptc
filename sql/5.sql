-- Migration: add etf_text to switch_events to record the plan's ETF at enrollment time
ALTER TABLE switch_events ADD COLUMN IF NOT EXISTS etf_text TEXT;
