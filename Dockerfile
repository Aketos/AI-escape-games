# Stage 1: Builder
FROM golang:alpine AS builder

WORKDIR /app

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the server binary statically
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/server

# Stage 2: Runner
FROM alpine:latest

WORKDIR /app

# Copy binary from builder
COPY --from=builder /server .

# Copy scenario configuration files into working directory
COPY function_calls.json player_state.json room_state.json ./

# Expose WebSocket port
EXPOSE 8080

# Run the server
CMD ["./server"]
