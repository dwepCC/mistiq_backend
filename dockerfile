# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/tukifac-api .

FROM alpine:3.21

RUN apk add --no-cache \
    ca-certificates \
    openssl \
    tzdata \
    wget \
    dumb-init \
    && addgroup -g 10001 -S app \
    && adduser -u 10001 -S -G app app

LABEL org.opencontainers.image.title="Tukifac Backend"
LABEL org.opencontainers.image.description="Tukifac Multi-tenant SaaS API"

WORKDIR /app

COPY --from=builder --chmod=755 /out/tukifac-api ./tukifac-api
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh

RUN chmod 755 /app/docker-entrypoint.sh \
    && mkdir -p uploads storage/invoices storage/saas \
    && chown -R app:app /app

USER app

ENV TZ=America/Lima
ENV PORT=3000

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
  CMD wget -qO- http://127.0.0.1:3000/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["dumb-init", "--", "/app/docker-entrypoint.sh"]

CMD ["./tukifac-api"]