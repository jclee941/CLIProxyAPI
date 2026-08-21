FROM golang:1.26-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/telegram-usage-bot ./cmd/telegram-usage-bot
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /telegram-usage-bot ./cmd/telegram-usage-bot

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /telegram-usage-bot /telegram-usage-bot

USER 65532:65532

ENTRYPOINT ["/telegram-usage-bot"]
