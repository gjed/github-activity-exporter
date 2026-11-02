# Runtime image for GoReleaser: the build context holds the prebuilt binary.
FROM gcr.io/distroless/static:nonroot
COPY renovate-exporter /usr/local/bin/renovate-exporter
ENV CONFIG_FILE=/etc/renovate-exporter/config.yaml
ENTRYPOINT ["/usr/local/bin/renovate-exporter"]
