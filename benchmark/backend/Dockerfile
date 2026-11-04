# Built with the benchmark module as its context: docker build -f backend/Dockerfile .
FROM golang:1.27-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
COPY cmd/benchmark-backend ./cmd/benchmark-backend
RUN CGO_ENABLED=0 go build -o /benchmark-backend ./cmd/benchmark-backend

FROM alpine:3.24

COPY --from=builder /benchmark-backend /benchmark-backend

ENTRYPOINT ["/benchmark-backend"]
