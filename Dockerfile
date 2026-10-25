# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
COPY assets ./assets
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mediacenter .

FROM alpine:3.20
RUN apk add --no-cache ffmpeg
COPY --from=build /out/mediacenter /usr/local/bin/mediacenter
ENV LIB_DIR=/data/library \
    CACHE_DIR=/data/.cache \
    THUMB_DIR=/data/.thumbs \
    PORT=8080
VOLUME ["/data/library", "/data/.cache", "/data/.thumbs"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/mediacenter"]
