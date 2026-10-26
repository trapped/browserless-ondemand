FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /ondemand .

FROM ghcr.io/browserless/chromium:v2.57.3
COPY --from=build /ondemand /usr/local/bin/browserless-ondemand
COPY LICENSE /usr/share/doc/browserless-ondemand/LICENSE
LABEL org.opencontainers.image.source="https://github.com/trapped/browserless-ondemand"
ENTRYPOINT ["/usr/local/bin/browserless-ondemand"]
CMD ["./scripts/start.sh"]
HEALTHCHECK --interval=30s --timeout=3s CMD curl -fsS http://127.0.0.1:${PORT:-3000}/_ondemand/health || exit 1
