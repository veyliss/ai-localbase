FROM golang:1.25.1-alpine3.22 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG APP_VERSION=dev
RUN go build -ldflags "-X ai-localbase/internal/version.Value=${APP_VERSION}" -o main . && \
    go build -o migrate-qdrant-vectors ./eval/cmd/migrate_qdrant_vectors

FROM alpine:3.22.1

RUN apk --no-cache add ca-certificates curl su-exec \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && mkdir -p /app/data \
    && chown -R app:app /app

WORKDIR /app

COPY --from=builder --chown=app:app /app/main .
COPY --from=builder --chown=app:app /app/migrate-qdrant-vectors .

ENTRYPOINT ["sh", "-c", "if [ ! -e /app/data/.ai-localbase-ownership-v1 ]; then chown -R app:app /app/data && touch /app/data/.ai-localbase-ownership-v1 && chown app:app /app/data/.ai-localbase-ownership-v1; fi && exec su-exec app:app \"$@\"", "--"]
CMD ["./main"]
