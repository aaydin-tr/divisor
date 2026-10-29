# The integration suite's divisor image, built from the repo root. Alpine and
# root on purpose: scenarios run /bin/sh to write configs and certs into /etc/divisor.
FROM golang:1.27-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /divisor .

FROM alpine:latest

RUN mkdir -p /etc/divisor

COPY --from=builder /divisor /divisor

EXPOSE 8080
ENTRYPOINT ["/divisor"]
CMD ["--config", "/etc/divisor/config.yaml"]
