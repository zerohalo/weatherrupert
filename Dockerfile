# Stage 1: Build the Go binary
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Download dependencies first (cached layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /weatherrupert .

# Stage 2: Minimal runtime image
FROM alpine:3.20

# FFmpeg for video encoding, ca-certificates for HTTPS, tzdata for local time display
RUN apk add --no-cache ffmpeg ca-certificates tzdata

WORKDIR /app

COPY --from=builder /weatherrupert .

EXPOSE 9798

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:9798/health || exit 1

ENTRYPOINT ["/app/weatherrupert"]
