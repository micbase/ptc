-- Migration: usage_intervals table for SmartMeterTexas 15-minute interval data
--
-- Source: POST https://www.smartmetertexas.com/api/usage/interval
--   request:  {"esiid":"...","startDate":"MM/DD/YYYY","endDate":"MM/DD/YYYY"}
--   response: {
--               "intervaldata": [
--                 {
--                   "date":                 "2026-03-24",
--                   "starttime":            " 12:00 am",
--                   "endtime":              " 12:15 am",
--                   "consumption":          0.36,
--                   "consumption_est_act":  "A",
--                   "generation":           0,
--                   "generation_est_act":   null
--                 }
--               ],
--               "generationFlag": false
--             }
CREATE TABLE IF NOT EXISTS usage_intervals (
    interval_start  timestamp   NOT NULL PRIMARY KEY,  -- CT local time (no timezone)
    consumption_kwh numeric(10,4),
    is_actual       boolean
);
