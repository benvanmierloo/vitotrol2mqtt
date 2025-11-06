FROM golang:1.21.1 AS builder

RUN CGO_ENABLED=0 GOOS=linux go install github.com/benvanmierloo/vitotrol2mqtt@latest
RUN CGO_ENABLED=0 GOOS=linux go install github.com/benvanmierloo/go-vitotrol/cmd/vitotrol@master

FROM alpine:3.18.3
RUN apk --no-cache add bash
COPY --from=builder /go/bin/vitotrol2mqtt /vitotrol2mqtt
COPY --from=builder /go/bin/vitotrol /vitotrol
RUN mkdir /config
COPY ./vitotrol2mqtt.yml /config/vitotrol2mqtt.yml
COPY ./vitotrol2mqtt.yml /vitotrol2mqtt.yml

# Define the entrypoint command using Bash
ENTRYPOINT ["/bin/bash", "-c", "while true; do /vitotrol2mqtt -config /config/vitotrol2mqtt.yml || sleep 5; done"]
