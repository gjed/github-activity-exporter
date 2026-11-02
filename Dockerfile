# Runtime image for GoReleaser: the build context holds the prebuilt binary.
FROM gcr.io/distroless/static:nonroot
COPY renovate-exporter /usr/local/bin/renovate-exporter
ENTRYPOINT ["/usr/local/bin/renovate-exporter"]
