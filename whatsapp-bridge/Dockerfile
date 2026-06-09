# syntax=docker/dockerfile:1.6
# Multi-stage build for the whatsapp-bridge Go binary.
# The whatsmeow library uses go-sqlite3 which requires CGO, so the build
# stage uses alpine with gcc/musl-dev and the runtime stage has sqlite-libs.

FROM golang:1.25-alpine AS build
RUN apk add --no-cache git gcc musl-dev sqlite-dev
# proxy.golang.org times out on TLS handshake from this VM (asia-southeast1).
# Fetch modules direct from each origin (github.com, go.mau.fi, golang.org/x)
# which are all reachable. GOSUMDB off because the sumdb is also fronted by
# the proxy.
ENV GOPROXY=direct
ENV GOSUMDB=off
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bridge .

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates sqlite-libs tzdata
COPY --from=build /bridge /usr/local/bin/bridge

# Bridge writes store/whatsapp.db and store/messages.db inside its
# working directory. /data is mounted as a volume in docker-compose so
# the session and message history survive container restarts.
VOLUME /data
WORKDIR /data

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/bridge"]
