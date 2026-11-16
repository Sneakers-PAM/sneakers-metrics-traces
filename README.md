# sneakers-metrics-traces 🐹

> 🧭 Starter observability for Sneakers: an OpenTelemetry Collector config, Grafana dashboards and a Docker Compose stack for the product's OTLP traces, metrics and logs, run beside the box.

## 📡 Collector

`collector/config.yaml` receives OTLP from the box over TLS (gRPC on 4317, HTTP on 4318) and sends
traces to Tempo, metrics to Prometheus (remote write) and logs to Loki. It is configured through
environment variables:

| Variable | Meaning |
|---|---|
| `OTLP_TLS_CERT_FILE`, `OTLP_TLS_KEY_FILE` | Server certificate and key for the OTLP receivers |
| `TEMPO_ENDPOINT` | Tempo OTLP gRPC address, such as `tempo:4317` |
| `PROMETHEUS_REMOTE_WRITE_URL` | Prometheus remote-write URL |
| `LOKI_ENDPOINT` | Loki OTLP HTTP base URL |
| `LOG_LEVEL` | Collector log level (default `info`) |

Point the chart's `global.otlpEndpoint` at the receiver, for example `https://collector.example.org:4317`.
No key is stored in this repo; the tests generate a throwaway certificate for `collector.example.org`.

## 🐳 Compose stack

`compose.yaml` runs the collector with Tempo, Prometheus, Loki and Grafana. Images are pinned.

```bash
docker compose up -d     # certs, collector, tempo, prometheus, loki, grafana
docker compose down -v   # stop and drop the data
```

A `certs` step generates a throwaway certificate for `collector.example.org` on first start
(`OTLP_TLS_HOSTNAME` changes the name); no key is stored in this repo. The collector listens on
4317 (gRPC) and 4318 (HTTP) over TLS, and Grafana is on <http://localhost:3000> with the three
data sources already provisioned. Backend settings live in `stack/`.

## 📊 Dashboards

`dashboards/sneakers-overview.json` is provisioned into Grafana by the Compose stack: gRPC and HTTP
rate, status and latency per service (a `Service` selector at the top), the logs from Loki and recent
traces from Tempo. HTTP latency reads the otelhttp histogram only, because the go-otel histogram of the
same name uses the SDK's default buckets, which suit milliseconds.

Every metric and label a dashboard queries is checked against the telemetry contract. `testdata/telemetry.json`
is `docs/telemetry.json` from sneakers-release at the ref in `testdata/telemetry.ref`; `go test ./...` fails when a
Prometheus query uses a name or label the contract does not list. The collector exports names as
Prometheus holds them: dots become underscores, the unit becomes a suffix (`_seconds`, `_bytes`),
counters end in `_total`, `service.name` is the `job` label and the instrumentation scope is `otel_scope_name`.
To follow a new release, copy its `docs/telemetry.json` and `docs/telemetry.ref` into `testdata/`.

## 🚢 Kind drift job

`.github/workflows/job-kind-drift.yaml` installs the sneakers-release chart on a throwaway kind cluster at the
ref in `testdata/telemetry.ref`, with every service exporting OTLP to an in-cluster collector (`kind/`), and
fails when what the nine services send has drifted from the telemetry contract. It runs by hand and on pull
requests that touch the kind files or the checker. This repo is optional, so the job is never a required check
and is not part of appliance CI.

It reuses the release repo's own install test (`test/kind/run.sh`) for the cluster, the images and the traffic.
The in-cluster collector takes plaintext OTLP, because the chart passes the services a plain `http://` endpoint;
the Compose stack keeps the TLS receiver. It writes every request to files, and a busybox sidecar on the same
volume is what `kubectl cp` reads, since the collector image has no shell or `tar`.

`internal/drift` fails the job on anything in the recording that the contract does not list: a service, metric,
scope, metric attribute, span name or resource attribute. It also reports a service with no metrics, no logs or,
where the contract lists span names, no traces. It asks for one of each, not every instrument, because most only
appear once the matching traffic runs. Log fields are not checked yet. A second recording is taken with one
service stopped and must hold a gRPC client call that did not end OK and a span in error status.

To run the check on a recording by hand:

```bash
RECORDING_DIR=./recordings/real RECORDING_MODE=real go test ./internal/drift -run 'TestRecording$' -v
```

## 🛠 Develop

```bash
task build    # go build ./...
task test     # go test ./... (validates the collector config in Docker)
task lint     # gofmt check + golangci-lint + yamllint
task license  # check Apache-2.0 headers (golic)
```

## ⚖️ License

Apache-2.0 (c) 2026 The Sneakers-PAM Authors
