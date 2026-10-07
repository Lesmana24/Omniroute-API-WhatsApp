# Multi-stage production build for Omniroute WhatsApp Integration Service
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install certificates and build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /app/bin/omniroute-wa ./cmd/api/main.go

# Production scratch/alpine container
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/bin/omniroute-wa /app/omniroute-wa
COPY --from=builder /app/migrations /app/migrations

EXPOSE 8080

ENTRYPOINT ["/app/omniroute-wa"]
