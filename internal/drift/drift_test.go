// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sneakers-PAM/sneakers-metrics-traces/internal/contract"
)

const (
	otelgrpc = "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	otelhttp = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func load(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.Load("../../testdata/telemetry.json")
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	return c
}

// res renders a resource with the given string attributes.
func res(service string, extra ...string) string {
	attrs := []string{fmt.Sprintf(`{"key":"service.name","value":{"stringValue":%q}}`, service)}
	for _, k := range extra {
		attrs = append(attrs, fmt.Sprintf(`{"key":%q,"value":{"stringValue":"x"}}`, k))
	}
	return `"resource":{"attributes":[` + strings.Join(attrs, ",") + `]}`
}

// metric is one OTLP metrics request line with one histogram point.
func metric(service, scope, name string, attrs map[string]string) string {
	var kv []string
	for k, v := range attrs {
		kv = append(kv, fmt.Sprintf(`{"key":%q,"value":{"stringValue":%q}}`, k, v))
	}
	return fmt.Sprintf(`{"resourceMetrics":[{%s,"scopeMetrics":[{"scope":{"name":%q},"metrics":[`+
		`{"name":%q,"histogram":{"dataPoints":[{"attributes":[%s]}]}}]}]}]}`,
		res(service), scope, name, strings.Join(kv, ","))
}

// span is one OTLP traces request line with one span. kind is the protobuf
// number the file exporter writes: 2 server, 3 client.
func span(service, name string, kind, status int) string {
	return fmt.Sprintf(`{"resourceSpans":[{%s,"scopeSpans":[{"spans":[{"name":%q,"kind":%d,"status":{"code":%d}}]}]}]}`,
		res(service), name, kind, status)
}

func logline(service string) string {
	return fmt.Sprintf(`{"resourceLogs":[{%s,"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`, res(service))
}

func parse(t *testing.T, traces, metrics, logs []string) *Recording {
	t.Helper()
	r, err := Parse(
		strings.NewReader(strings.Join(traces, "\n")),
		strings.NewReader(strings.Join(metrics, "\n")),
		strings.NewReader(strings.Join(logs, "\n")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

const grpcSpan = "sneakers.vault.v1.VaultService/Get"

func TestACleanRecordingHasNoDrift(t *testing.T) {
	r := parse(t,
		[]string{span("identity", grpcSpan, 2, 0), span("identity", "query SELECT", 3, 0)},
		[]string{metric("identity", otelgrpc, "rpc.server.call.duration", map[string]string{"rpc.method": "Get", "rpc.response.status_code": "OK"})},
		[]string{logline("identity")})
	if got := r.Drift(load(t)); len(got) != 0 {
		t.Fatalf("unexpected drift: %v", got)
	}
}

func TestDriftFlagsEachKindOfChange(t *testing.T) {
	tests := []struct {
		name    string
		traces  []string
		metrics []string
		logs    []string
		want    string
	}{
		{"an unknown service", nil, nil, []string{logline("billing")}, `service "billing"`},
		{"a renamed metric", nil, []string{metric("identity", otelgrpc, "rpc.server.call.latency", nil)}, nil, `metric "rpc.server.call.latency"`},
		{"a metric under another scope", nil, []string{metric("identity", "other.scope", "rpc.server.call.duration", nil)}, nil, `scope "other.scope"`},
		{"a metric the service does not export", nil, []string{metric("gateway", otelgrpc, "rpc.server.call.duration", nil)}, nil, `service "gateway"`},
		{"a new metric attribute", nil, []string{metric("identity", otelgrpc, "rpc.server.call.duration", map[string]string{"user.id": "u"})}, nil, `attribute "user.id"`},
		{"an unlisted span name", []string{span("identity", "made-up", 2, 0)}, nil, nil, `span "made-up"`},
		{"a span of the wrong kind", []string{span("identity", "query SELECT", 2, 0)}, nil, nil, `span "query SELECT"`},
		{"spans from a service that has none", []string{span("mcp", grpcSpan, 2, 0)}, nil, nil, `service "mcp"`},
		{"an extra resource attribute", nil, nil, []string{`{"resourceLogs":[{` + res("identity", "host.name") + `,"scopeLogs":[]}]}`}, `resource attribute "host.name"`},
	}
	c := load(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parse(t, tt.traces, tt.metrics, tt.logs).Drift(c)
			if len(got) != 1 || !strings.Contains(got[0], tt.want) {
				t.Fatalf("want one problem containing %s, got %v", tt.want, got)
			}
		})
	}
}

func TestDriftAcceptsAnyOfTheServicesMetricsAndScopes(t *testing.T) {
	r := parse(t, nil, []string{
		metric("gateway", otelhttp, "http.server.request.duration", map[string]string{"http.route": "/x", "http.response.status_code": "200"}),
		metric("gateway", "github.com/Bugs5382/go-otel", "http.server.request.duration", nil),
	}, nil)
	if got := r.Drift(load(t)); len(got) != 0 {
		t.Fatalf("unexpected drift: %v", got)
	}
}

// completeRecording has traffic from every service.
func completeRecording(t *testing.T, c *contract.Contract) *Recording {
	t.Helper()
	var traces, metrics, logs []string
	for _, name := range c.ServiceNames() {
		s := c.Service(name)
		if s.ExportsSpans() {
			traces = append(traces, span(name, grpcSpan, 2, 0))
		}
		metrics = append(metrics, metric(name, s.Metrics[0].Scope, s.Metrics[0].Name, nil))
		logs = append(logs, logline(name))
	}
	return parse(t, traces, metrics, logs)
}

func TestMissingIsEmptyForACompleteRecording(t *testing.T) {
	c := load(t)
	if got := completeRecording(t, c).Missing(c); len(got) != 0 {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestMissingNamesTheServiceAndTheSignal(t *testing.T) {
	c := load(t)
	// Only identity reports, with logs and metrics but no spans.
	r := parse(t, nil,
		[]string{metric("identity", otelgrpc, "rpc.server.call.duration", nil)},
		[]string{logline("identity")})
	got := strings.Join(r.Missing(c), "\n")
	for _, want := range []string{"identity: no traces", "vault: no metrics", "vault: no logs", "gateway: no traces"} {
		if !strings.Contains(got, want) {
			t.Errorf("Missing must report %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mcp: no traces") {
		t.Errorf("mcp exports no spans, so a missing trace is not a problem:\n%s", got)
	}
}

func TestMissingDoesNotRequireEveryMetricOfAService(t *testing.T) {
	c := load(t)
	// identity lists two metrics; one is enough, because the other depends on traffic.
	r := completeRecording(t, c)
	if got := r.Missing(c); len(got) != 0 {
		t.Fatalf("a service with one of its metrics must pass: %v", got)
	}
}

func TestFailureSignalsNeedAClientErrorAndAnErrorSpan(t *testing.T) {
	healthy := parse(t,
		[]string{span("gateway", grpcSpan, 3, 0)},
		[]string{metric("gateway", otelgrpc, "rpc.client.call.duration", map[string]string{"rpc.response.status_code": "OK"})},
		nil)
	if got := healthy.FailureSignals(); len(got) != 2 {
		t.Fatalf("a healthy recording must report both signals missing, got %v", got)
	}

	down := parse(t,
		[]string{span("gateway", grpcSpan, 3, 2)},
		[]string{metric("gateway", otelgrpc, "rpc.client.call.duration", map[string]string{"rpc.response.status_code": "UNAVAILABLE"})},
		nil)
	if got := down.FailureSignals(); len(got) != 0 {
		t.Fatalf("a stopped-service recording must pass, got %v", got)
	}
}

func TestParseRejectsBrokenJSON(t *testing.T) {
	if _, err := Parse(strings.NewReader("{not json"), strings.NewReader(""), strings.NewReader("")); err == nil {
		t.Fatal("expected an error for a broken line")
	}
}

func TestParseAcceptsEmptyInput(t *testing.T) {
	r := parse(t, nil, nil, nil)
	if got := r.Drift(load(t)); len(got) != 0 {
		t.Fatalf("an empty recording has no drift: %v", got)
	}
}
