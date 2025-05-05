FROM golang:alpine as builder

WORKDIR /app
COPY . .

RUN GOOS=linux go build -o app .

FROM alpine:latest
run mkdir -p /opt/pushgateway
WORKDIR /opt/pushgateway
COPY --from=builder /app/app .

ENTRYPOINT ["./app"]
