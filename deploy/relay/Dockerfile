# flok-relay: the service between flok instances and the phone. One static binary on scratch
# plus the CA bundle (APNs is TLS). Built per architecture from a cross-compiled binary, so no
# emulation is needed: `make relay-image` or the release workflow.
#
#   docker build -f deploy/relay/Dockerfile --build-arg BIN=dist/flok-relay_linux_amd64 -t flok-relay .
#
# BIN is a prebuilt linux binary for the target platform; without it the image builds it here.
ARG BIN=
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH BIN
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -eu; \
    if [ -n "$BIN" ] && [ -f "$BIN" ]; then cp "$BIN" /flok-relay; \
    else CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" -o /flok-relay ./cmd/flok-relay; fi; \
    chmod 755 /flok-relay; \
    mkdir -p /data && chown 65532:65532 /data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /flok-relay /flok-relay
# /data is owned by the relay's user in the image, so a fresh named volume mounted there takes
# that ownership and devices.json can be written (a volume over a missing path is root's)
COPY --from=build --chown=65532:65532 /data /data
USER 65532:65532
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/flok-relay"]
CMD ["serve", "-listen", ":8080", "-data", "/data"]
