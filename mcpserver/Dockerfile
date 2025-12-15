# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the MCP server binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o mattermost-mcp-server ./mcpserver/cmd/main.go

# Runtime stage
FROM alpine:3.19

# Install CA certificates for HTTPS connections to Mattermost
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /build/mattermost-mcp-server /usr/local/bin/

# Run as non-root user
RUN adduser -D -u 1000 mcp
USER mcp

ENTRYPOINT ["mattermost-mcp-server"]
