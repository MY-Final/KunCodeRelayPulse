# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-build

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend-build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-build /src/internal/web/static ./internal/web/static
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/pulse ./cmd/pulse

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 pulse \
    && adduser -S -u 10001 -G pulse pulse

WORKDIR /app
COPY --from=backend-build /out/pulse /usr/local/bin/pulse
COPY --from=backend-build /src/config.example.yaml /app/config.example.yaml
COPY --from=backend-build /src/channels.d /app/channels.d
COPY --from=backend-build /src/proxies.d /app/proxies.d
COPY --from=backend-build /src/templates /app/templates
RUN mkdir -p /app/data \
    && chown -R pulse:pulse /app

USER pulse
EXPOSE 18080
VOLUME ["/app/data", "/app/channels.d", "/app/proxies.d"]
ENTRYPOINT ["/usr/local/bin/pulse"]
CMD ["-config", "/app/config.yaml"]
