FROM golang:1.20-alpine

WORKDIR /server

COPY . .

RUN go mod download

RUN go build -o /app

EXPOSE 8080

CMD [ "/app" ]