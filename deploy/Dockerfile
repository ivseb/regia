# syntax=docker/dockerfile:1
# Immagine unica di Regia: binario Go + Postgres 18 incluso, gestito dal binario.
# Lo stesso artefatto serve per SaaS (con DATABASE_URL esterno) e on-premise (Postgres incluso).
#
# Base Alpine minimale: di Postgres si copiano solo i programmi che il binario usa
# (postgres, initdb, pg_dump, pg_restore), i moduli e i file di share, e si installano solo
# le librerie a cui sono collegati. Circa 100 MB su disco invece dei 695 MB dell'immagine
# pgvector/pgvector:pg18 usata prima (misure in docs/operativo.md).
#
#   docker build -f deploy/Dockerfile -t regia .                       # con pgvector
#   docker build -f deploy/Dockerfile --build-arg PGVECTOR=false -t regia:slim .
#
# Senza pgvector lo schema funziona lo stesso (la tabella degli embedding nasce solo se
# l'estensione è disponibile) e la ricerca semantica resta spenta.

ARG ALPINE_VERSION=3.24

FROM node:24-alpine AS web
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY web/ ./
COPY scripts/check-contrast.mjs scripts/gen-i18n.mjs /src/scripts/
RUN pnpm build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY --from=web /src/internal/webui/dist/app ./internal/webui/dist/app
ARG VERSION=dev
ARG COMMIT=sconosciuto
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o /regia ./cmd/regia

# Pacchetti di Postgres 18 di Alpine: da qui si prende solo ciò che serve.
FROM alpine:${ALPINE_VERSION} AS pg
ARG PGVECTOR=true
RUN apk add --no-cache postgresql18 postgresql18-contrib postgresql18-client \
 && if [ "$PGVECTOR" = "true" ]; then apk add --no-cache postgresql-pgvector; fi \
 && mkdir -p /out/bin \
 && cd /usr/libexec/postgresql18 && cp -p postgres initdb pg_dump pg_restore /out/bin/

FROM alpine:${ALPINE_VERSION}
# Librerie a cui sono collegati postgres, initdb, pg_dump, pg_restore e le estensioni usate
# (pgcrypto, unaccent, pg_trgm, vector): verificate con ldd. tini fa da processo 1.
RUN apk add --no-cache icu-libs libldap libpq libxml2 liburing lz4-libs zstd-libs xz-libs \
        libstdc++ libgcc tzdata tini \
 && addgroup -S -g 10001 regia \
 && adduser -S -D -H -u 10001 -G regia -h /data -s /sbin/nologin regia \
 && mkdir -p /data && chown regia:regia /data && chmod 700 /data
COPY --from=pg /out/bin/ /usr/libexec/postgresql18/
COPY --from=pg /usr/lib/postgresql18/ /usr/lib/postgresql18/
COPY --from=pg /usr/share/postgresql18/ /usr/share/postgresql18/
COPY --from=go /regia /usr/local/bin/regia
COPY LICENSE NOTICE /usr/share/licenses/regia/
LABEL org.opencontainers.image.licenses="AGPL-3.0-only"
ENV REGIA_DATA_DIR=/data \
    REGIA_PG_BIN=/usr/libexec/postgresql18 \
    REGIA_HTTP_ADDR=:8080 \
    REGIA_LOG_FORMAT=json
# Postgres non può girare come root: tutto gira come utente regia (uid 10001).
USER regia
VOLUME ["/data"]
EXPOSE 8080
# Il primo avvio fa initdb e le migrazioni; un aggiornamento fa prima il backup automatico.
HEALTHCHECK --interval=30s --timeout=3s --start-period=120s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
# tini è il processo 1: inoltra SIGTERM a regia e raccoglie i processi orfani. regia chiude
# le richieste aperte (fino a 20 s) e poi ferma Postgres (fino a 20 s): serve
# `docker stop -t 60` o `stop_grace_period: 60s`, perché il default di 10 s può interromperlo.
STOPSIGNAL SIGTERM
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/regia"]
CMD ["serve"]
