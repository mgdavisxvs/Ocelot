# Multi-stage Dockerfile for Ocelot Tracker

# Build stage
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

WORKDIR /build

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary with optimizations
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo \
    -ldflags="-w -s -X main.Version=$(git describe --tags --always --dirty) -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o ocelot-tracker .

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    && addgroup -g 1000 ocelot \
    && adduser -D -u 1000 -G ocelot ocelot

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/ocelot-tracker .

# Create data directory
RUN mkdir -p /app/data && chown -R ocelot:ocelot /app

# Switch to non-root user
USER ocelot

# Expose ports
EXPOSE 34000 9090

# Health check
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:34000/health || exit 1

# Run tracker
CMD ["./ocelot-tracker"]
