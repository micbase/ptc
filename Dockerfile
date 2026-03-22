# Stage 1: Build frontend
FROM node:24-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/ .
RUN npm ci && npm run build

# Stage 2: Build backend
FROM golang:1.26-alpine AS backend
WORKDIR /app
COPY backend/ ./backend/
COPY --from=frontend /app/frontend/dist ./backend/dist
WORKDIR /app/backend
RUN go build -o /ptc .

# Stage 3: Runtime
FROM alpine:3.21
COPY --from=backend /ptc /ptc
EXPOSE 8080
CMD ["/ptc"]
