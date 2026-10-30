# syntax=docker/dockerfile:1
# Multi-arch: docker buildx build --platform linux/amd64,linux/arm64 -t danmu-bridge .
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/danmu-bridge ./cmd/danmu-bridge

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/danmu-bridge /usr/local/bin/danmu-bridge
# Same uid/gid as the linuxserver containers (PUID/PGID=1000) so the xml it
# writes into the library is owned like the rest of the library.
USER 1000:1000
ENV STATE_DIR=/data LISTEN_ADDR=:8090
VOLUME ["/data"]
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/danmu-bridge"]
CMD ["serve"]
