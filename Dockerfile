# syntax=docker/dockerfile:1

# Production image: built once and pulled by the server, which never compiles.

FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22 AS runner
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S -G app app \
    && mkdir -p /app/data/photos && chown -R app:app /app/data
WORKDIR /app
COPY --from=builder /out/api /app/api
USER app
ENV PORT=3001 APP_ENV=production PHOTOS_DIR=/app/data/photos
EXPOSE 3001
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["/app/api", "healthcheck"]
ENTRYPOINT ["/app/api"]
