FROM golang:1.24 AS backend-builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY internal/ ./internal/

RUN GOOS=linux go build -o mockmt .

FROM node:24 AS frontend-builder

WORKDIR /app/frontend

COPY frontend/package*.json ./
RUN npm ci

COPY frontend/ ./

RUN npm run build

FROM gcr.io/distroless/base-debian12

WORKDIR /app

COPY --from=backend-builder /app/mockmt .

COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

COPY env.example .env

ENV GIN_MODE=release
ENV PORT=8080
ENV SMTP_PORT=1025
ENV DB_PATH=/app/data/webmail.db
ENV FRONTEND_URL=http://localhost:8080
ENV SERVE_FRONTEND_DIST=true

EXPOSE 8080 1025

CMD ["./mockmt"]