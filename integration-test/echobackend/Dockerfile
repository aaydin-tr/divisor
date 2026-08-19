FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY . .
RUN CGO_ENABLED=0 go build -o /echobackend .

FROM alpine:latest

COPY --from=builder /echobackend /echobackend

EXPOSE 8080
ENTRYPOINT ["/echobackend"]
