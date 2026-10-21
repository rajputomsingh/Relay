# syntax=docker/dockerfile:1

# =========================
# Build Stage
# =========================
FROM golang:1.27-alpine AS builder

WORKDIR /src

# Install CA certificates for the build
RUN apk add --no-cache ca-certificates tzdata

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /relay ./cmd/relay

# =========================
# Production Runner
# =========================
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy timezone data and CA certs from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /relay /app/relay

# Run as the nonroot user (UID 65532)
USER nonroot:nonroot

# Expose the default port
EXPOSE 8080

ENTRYPOINT ["/app/relay"]
