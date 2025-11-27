# Build stage
FROM golang:1.25-alpine AS builder

# Build arguments
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG BUILD_TIME
ARG GIT_COMMIT

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with embedded assets and inject version info
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -extldflags '-static' \
        -X 'cfui/version.Version=${VERSION}' \
        -X 'cfui/version.BuildTime=${BUILD_TIME}' \
        -X 'cfui/version.GitCommit=${GIT_COMMIT}'" \
    -o cfui .

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS connections
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Set working directory
WORKDIR /app

# Copy binary from builder (assets are embedded)
COPY --from=builder /build/cfui .

# Create data and logs directories with proper ownership
RUN mkdir -p /app/data /app/logs && \
    chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 14333

# Set environment variables
ENV PORT=14333
ENV DATA_DIR=/app/data
ENV LOG_DIR=/app/logs

# Health check - uses PORT environment variable
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:${PORT}/ || exit 1

# Run the application
CMD ["./cfui"]
