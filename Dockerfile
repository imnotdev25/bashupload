# Build stage
FROM golang:1.21-alpine AS builder

# Install git for go mod download
RUN apk add --no-cache git

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

# Build the server
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o server .

# Build the CLI
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o uploader ./cmd/cli

# Runtime stage
FROM alpine:latest

# Install ca-certificates and sqlite
RUN apk --no-cache add ca-certificates sqlite

# Create non-root user
RUN adduser -D -s /bin/sh bashupload

# Set working directory
WORKDIR /app

# Copy binaries from builder
COPY --from=builder /app/server .
COPY --from=builder /app/uploader .

# Copy template and static files
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static

# Create uploads directory
RUN mkdir -p uploads && chown bashupload:bashupload uploads

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
# ENV API_KEY=your_secret_api_key_here

# Run bashupload server
CMD ["./server"]