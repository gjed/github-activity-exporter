# Build on the native platform and cross-compile for the target one: no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/github-activity-exporter ./cmd/github-activity-exporter

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/github-activity-exporter /usr/local/bin/github-activity-exporter
ENV CONFIG_FILE=/etc/github-activity-exporter/config.yaml
ENTRYPOINT ["/usr/local/bin/github-activity-exporter"]
