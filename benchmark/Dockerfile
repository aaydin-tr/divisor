FROM golang:1.25-alpine AS builder
WORKDIR /server

COPY . .

RUN go mod download

RUN go build -o /app

FROM alpine:latest

COPY --from=builder /app /app

EXPOSE 8080
CMD ["/app"]