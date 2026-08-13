# syntax=docker/dockerfile:1

# Go 1.25 is not optional: Fiber v3.4.0 refuses to build on 1.24.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Dependencies first, as their own layer: editing a .go file then does not
# re-download the module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
# docs/ is a build input, not documentation the image happens to carry: docs/docs.go
# embeds openapi.yaml so the server can serve Swagger UI. Omitting it fails the
# build.
COPY docs ./docs

# CGO_ENABLED=0 gives static binaries. It costs nothing here — pgx is pure Go and
# there is no sqlite or libpq in the tree — and it is what lets the final stage be
# a bare alpine with no libc juggling.
#
# -trimpath keeps build paths out of the binary; -s -w drop the symbol and DWARF
# tables, which are dead weight in a container image.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/web ./cmd/web && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# base carries everything both entrypoints share. web and worker below differ only
# in their command and healthcheck, so they reuse these layers rather than
# duplicating them.
FROM alpine:3.21 AS base

# ca-certificates for any future outbound HTTPS (the repository layer is allowed to
# call upstream HTTP). tzdata because report boundaries are local-time questions
# even though every stored instant is TIMESTAMPTZ.
RUN apk add --no-cache ca-certificates tzdata

# Runs unprivileged. The one thing this user writes is /app/data/dokumen — uploaded
# attachments — so that directory is created here and given to it. Logs still go to
# stdout.
RUN addgroup -S app && adduser -S -G app -h /app app

# Created in the image, not left to the volume: Docker seeds a named volume from
# whatever the image has at the mount point, ownership included. Without this the
# volume is created owned by root and the unprivileged process cannot write into it —
# and the failure surfaces at the first upload, not at boot.
#
# 0700 because attachments carry purchase prices and supplier identities.
RUN mkdir -p /app/data/dokumen && chown -R app:app /app/data && chmod 700 /app/data/dokumen

WORKDIR /app

COPY --from=builder /out/web /out/worker /app/

# config.NewViper panics if config.json is absent, and config.json is gitignored
# because it holds credentials. So the image ships the tracked example, and every
# value in it is overridden by environment variables at run time
# (database.host -> DATABASE_HOST). The example's own values are never the ones
# used in a container.
COPY config.example.json /app/config.json

USER app

FROM base AS web

EXPOSE 3000

# Hits the route registered in route.setupGuestRoute. wget is busybox's, already in
# alpine. 127.0.0.1 rather than localhost: the app binds 0.0.0.0 and this avoids
# depending on how the image resolves localhost.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD wget -qO- http://127.0.0.1:3000/health >/dev/null 2>&1 || exit 1

CMD ["/app/web"]

FROM base AS worker

# No healthcheck: the worker serves no HTTP. It runs its jobs on a ticker and is
# idle in between, so a container sitting quiet is the expected state — check the
# logs for "worker: job selesai" rather than the process.
CMD ["/app/worker"]
