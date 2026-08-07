FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY mockoauth/ ./mockoauth/

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/mockoauth ./mockoauth

# alpine, not distroless/scratch: BusyBox's wget is what the compose
# healthcheck uses to probe readiness (research R11).
FROM alpine:3.20

COPY --from=builder /out/mockoauth /usr/local/bin/mockoauth

EXPOSE 9000

CMD ["mockoauth"]
