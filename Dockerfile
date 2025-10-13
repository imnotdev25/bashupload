# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies for CGO and SQLite
RUN apk add --no-cache \
    git \
    gcc \
    g++ \
    musl-dev \
    sqlite-dev \
    make

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Create required directories
RUN mkdir -p templates static

# Build the server (CGO enabled for SQLite with musl compatibility tags)
# Use native architecture instead of forcing amd64
RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" \
    go build -a \
    -tags "sqlite_omit_load_extension" \
    -ldflags="-w -s -extldflags '-static'" \
    -o server .

# Build the CLI (CGO enabled for SQLite with musl compatibility tags)
RUN CGO_ENABLED=1 CGO_CFLAGS="-D_LARGEFILE64_SOURCE" \
    go build -a \
    -tags "sqlite_omit_load_extension" \
    -ldflags="-w -s -extldflags '-static'" \
    -o bashupload ./cmd/cli

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    tzdata

# Create non-root user
RUN adduser -D -s /bin/sh bashupload

# Set working directory
WORKDIR /app

# Copy binaries from builder
COPY --from=builder /app/server ./server
COPY --from=builder /app/bashupload ./bashupload

# Copy template and static files
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# Create uploads directory
RUN mkdir -p uploads && chown -R bashupload:bashupload uploads

# Change ownership of app directory
RUN chown -R bashupload:bashupload /app

# Switch to non-root user
USER bashupload

# Expose port
EXPOSE 3000

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/ || exit 1

# Set environment variables
ENV PORT=3000
ENV GIN_MODE=release
ENV MAX_UPLOAD_SIZE=1GB
ENV MAX_DOWNLOADS=1
ENV FILE_EXPIRE_AFTER=3D
# ENV API_KEY=your_secret_api_key_here

# Run bashupload server
CMD ["./server"]