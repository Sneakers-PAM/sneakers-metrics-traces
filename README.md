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

## 🛠 Develop

```bash
task build    # go build ./...
task test     # go test ./... (validates the collector config in Docker)
task lint     # gofmt check + golangci-lint + yamllint
task license  # check Apache-2.0 headers (golic)
```

## ⚖️ License

Apache-2.0 (c) 2026 The Sneakers-PAM Authors
