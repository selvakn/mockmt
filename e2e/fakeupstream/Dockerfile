FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY fakeupstream/ ./fakeupstream/

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/fakeupstream ./fakeupstream

# alpine, not distroless/scratch: BusyBox's nc is what the compose
# healthcheck uses to probe readiness (research R11).
FROM alpine:3.20

COPY --from=builder /out/fakeupstream /usr/local/bin/fakeupstream

EXPOSE 587

CMD ["fakeupstream"]
