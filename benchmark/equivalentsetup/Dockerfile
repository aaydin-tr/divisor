# Built with the benchmark module as its context: docker build -f equivalentsetup/Dockerfile .
FROM golang:1.27-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY backend ./backend
COPY contenders ./contenders
COPY equivalentsetup ./equivalentsetup
COPY oha ./oha
COPY plan ./plan
COPY recording ./recording
COPY cmd/equivalent-setup-sampler ./cmd/equivalent-setup-sampler
RUN CGO_ENABLED=0 go build -o /equivalent-setup-sampler ./cmd/equivalent-setup-sampler

FROM alpine:3.24

COPY --from=builder /equivalent-setup-sampler /equivalent-setup-sampler

ENTRYPOINT ["/equivalent-setup-sampler"]
