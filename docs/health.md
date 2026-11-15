# Health endpoints

The exporter always serves `/healthz` and `/readyz` on `health.listen_address` (default `:10062`), whether or not the Prometheus bridge is enabled. The server starts before the startup credential checks. Both endpoints answer `GET` and `HEAD` with `200 ok`, or `503` with one line per failing target and the reason.

`/readyz` returns 200 once every target has passed its startup credential and [permission](github-permissions.md) checks and completed at least one collection cycle in which repo discovery succeeded. Failures of single repos do not block it. Once ready, the exporter stays ready.

`/healthz` returns 503 when, for any target:

- its collection loop exited before shutdown;
- no collection cycle completed within `health.stall_timeout` of the previous one, or of the loop start. A cycle completes at most `collection_interval` plus its own duration after the previous one, so the default, `2 × collection_interval + 1h` (1h10m at 5m), allows a cycle of up to `collection_interval + 1h`: room for a full GitHub rate-limit pause;
- its last `health.unauthorized_cycles` cycles (default 3) were all unauthorized. A cycle is unauthorized when GitHub answered at least one request, including the GitHub App token exchange, with 401 and none with any other status. A cycle with any non-401 response clears the count; a cycle with no GitHub response leaves it as is.

A 401 never affects `/readyz`. A credential revoked while running fails `/healthz`; after a restart the startup credential check fails and the exporter exits.

The Prometheus bridge serves only `/metrics`.
