-- Create database (run this first if database doesn't exist)
-- CREATE DATABASE electricity_db;

-- Connect to the database and create the table
CREATE TABLE IF NOT EXISTS electricity_rates (
    id SERIAL PRIMARY KEY,
    id_key TEXT,
    tdu_company_name VARCHAR(255),
    rep_company VARCHAR(255),
    product VARCHAR(500),
    kwh500 DECIMAL(10,4),
    kwh1000 DECIMAL(10,4),
    kwh2000 DECIMAL(10,4),
    fees_credits TEXT,
    prepaid BOOLEAN,
    time_of_use BOOLEAN,
    fixed INTEGER,
    rate_type VARCHAR(50),
    renewable INTEGER,
    term_value INTEGER,
    cancel_fee VARCHAR(500),
    website VARCHAR(500),
    special_terms TEXT,
    terms_url VARCHAR(500),
    yrac_url VARCHAR(500),
    promotion BOOLEAN,
    promotion_desc TEXT,
    facts_url VARCHAR(500),
    enroll_url VARCHAR(500),
    prepaid_url VARCHAR(500),
    enroll_phone VARCHAR(50),
    new_customer BOOLEAN,
    min_usage_fees_credits BOOLEAN,
    language VARCHAR(50),
    rating INTEGER,
    fetch_date DATE NOT NULL,
    processed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Create unique constraint to prevent duplicate entries for same id_key on same date
    UNIQUE(id_key, fetch_date)
);

-- Create indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_electricity_rates_id_key ON electricity_rates(id_key);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_company ON electricity_rates(rep_company);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_tdu ON electricity_rates(tdu_company_name);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_rate_type ON electricity_rates(rate_type);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_term_value ON electricity_rates(term_value);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_renewable ON electricity_rates(renewable);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_rating ON electricity_rates(rating);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_fetch_date ON electricity_rates(fetch_date);
CREATE INDEX IF NOT EXISTS idx_electricity_rates_processed_at ON electricity_rates(processed_at);
