FROM golang:1-alpine3.19 AS builder

RUN apk add --no-cache git ca-certificates build-base su-exec olm-dev

COPY . /build
WORKDIR /build

ENV GOCACHE=/root/.cache/go-build

RUN --mount=type=cache,target="/root/.cache/go-build" go build -o /usr/bin/matrix-tinder .
RUN --mount=type=cache,target="/root/.cache/go-build" go install github.com/mattn/goreman@latest


FROM alpine:3.19

WORKDIR /app

ENV UID=1337 \
    GID=1337

RUN apk add --no-cache ffmpeg su-exec ca-certificates olm bash jq yq curl

COPY --from=builder /go/bin/goreman /usr/bin/goreman
COPY --from=builder /usr/bin/matrix-tinder /usr/bin/matrix-tinder
COPY --from=builder /build/Procfile .

VOLUME /data

CMD ["/usr/bin/goreman", "start"]
