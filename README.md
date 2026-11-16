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

## 🛠 Develop

```bash
task build    # go build ./...
task test     # go test ./... (validates the collector config in Docker)
task lint     # gofmt check + golangci-lint + yamllint
task license  # check Apache-2.0 headers (golic)
```

## ⚖️ License

Apache-2.0 (c) 2026 The Sneakers-PAM Authors
