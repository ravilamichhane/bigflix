# syntax=docker/dockerfile:1

FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN rm -rf ./internal/web/dist
COPY --from=frontend /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bigflix ./cmd/bigflix

FROM alpine:3.21
RUN apk add --no-cache ffmpeg ca-certificates
WORKDIR /app
COPY --from=backend /out/bigflix /usr/local/bin/bigflix
RUN mkdir -p /data /media/torrents

# Optional runtime secrets & tuning (not baked into image; most are also editable in Settings UI):
#   -e AUTH_TOKEN=<secret>           (defaults to "bigflix")
#   -e TMDB_READ_TOKEN=<token>       (TMDB API Read Access Token for Movies & TV catalog)
#   -e AI_API_URL=http://llama-server:8080/v1  (optional local CPU AI classifier)
#   -e SUBDL_API_KEY=<key>           (optional online TV/movie subtitle search)
#   -e OPENSUBTITLES_API_KEY=<key>   (optional online TV/movie subtitle search)
# Override storage paths with -e DATA_DIR=... -e MEDIA_DIR=... or mount directly to /data and /media.
ENV PORT=3000 \
    BIND_ADDR=0.0.0.0 \
    TORRENT_LISTEN_PORT=51413 \
    DATA_DIR=/data \
    MEDIA_DIR=/media/torrents \
    YTS_API_URL=https://movies-api.accel.li/api/v2/ \
    MEDIA_BUDGET_GB=50 \
    MIN_FREE_GB=10 \
    FFMPEG_THREADS=2

EXPOSE 3000/tcp 51413/tcp 51413/udp
VOLUME ["/data", "/media"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -qO- "http://127.0.0.1:${PORT}/" >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/bigflix"]
