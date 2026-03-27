# PTC — Power to Choose Rate Comparison

Standalone rebuild of the Retool-based ONCOR electricity rate comparison dashboard.

## Tech Stack
- **Backend:** Go 1.24, Chi router, pgx (PostgreSQL)
- **Frontend:** Vue 3, Vite, Tailwind CSS, ECharts
- **Deploy:** Docker multi-stage build, GitHub Actions → GHCR

## Local Development

### Backend
```bash
cd backend
DATABASE_URL="postgres://user:pass@host:5432/dbname" go run .
```
Serves on `:8080` (override with `PORT` env var).

### Frontend (dev mode)
```bash
cd frontend
npm install
npm run dev
```
Vite dev server proxies `/api` to the backend.

### Build
```bash
cd frontend && npm run build   # outputs to dist/
cd backend && go build -o ptc .
docker build -t ptc .
```

## API Endpoints

### `GET /api/plans?date=YYYY-MM-DD`
Returns all ONCOR plans for the given date, sorted by kwh1000 ascending.

### `GET /api/charts?type=best|best_3m|variable`
Returns time-series `{fetch_date, kwh1000}` data for chart lines.

## Database
Requires an existing PostgreSQL `electricity_rates` table. Migration files are in `sql/` (numbered sequentially).

### Schema change rules
- **Always** create a numbered migration file in `sql/` for any schema change — never apply schema changes only in Go code.
- **Always** get explicit user approval before making any schema change.
