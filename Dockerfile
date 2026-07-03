# Go services image — builds the signing-router, deposit-svc, demo, and webui
# binaries into one small Alpine image. Each container runs a different binary
# via its `command:`.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off: go-ethereum's crypto uses its pure-Go secp256k1 fallback, so the
# image stays static and Alpine-friendly.
RUN CGO_ENABLED=0 go build -trimpath -o /out/ ./cmd/...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/ /usr/local/bin/
# webui serves the static UI from this directory.
COPY --from=build /src/webui/static /app/webui/static
WORKDIR /app
# Default; overridden per service in compose / k8s.
CMD ["demo"]
