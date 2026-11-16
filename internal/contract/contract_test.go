// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"os"
	"strings"
	"testing"
)

const (
	contractFile = "../../testdata/telemetry.json"
	refFile      = "../../testdata/telemetry.ref"
)

func load(t *testing.T) *Contract {
	t.Helper()
	c, err := Load(contractFile)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestLoadReadsTheRefAndAllNineServices(t *testing.T) {
	c := load(t)
	raw, err := os.ReadFile(refFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(raw)); c.Ref != want {
		t.Errorf("ref = %q, want %q", c.Ref, want)
	}
	names := c.ServiceNames()
	if len(names) != 9 {
		t.Fatalf("want nine services, got %d: %v", len(names), names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("ServiceNames must be sorted and unique: %v", names)
		}
	}
}

func TestServiceMetricsAreNameAndScope(t *testing.T) {
	gw := load(t).Service("gateway")
	if gw == nil {
		t.Fatal("no gateway service")
	}
	const otelhttp = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	if !gw.HasMetric("http.server.request.duration", otelhttp) {
		t.Error("gateway must export http.server.request.duration from otelhttp")
	}
	if gw.HasMetric("http.server.request.duration", "some.other.scope") {
		t.Error("a metric under another scope must not match")
	}
	if gw.HasMetric("rpc.server.call.duration", "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc") {
		t.Error("the gateway serves HTTP, not gRPC; it must not list rpc.server.call.duration")
	}
}

func TestSpanNamesMatchByPatternAndKind(t *testing.T) {
	c := load(t)
	id := c.Service("identity")
	if !id.MatchSpan("sneakers.vault.v1.VaultService/Get", "SERVER") {
		t.Error("a gRPC server span must match")
	}
	if !id.MatchSpan("query SELECT", "CLIENT") {
		t.Error("a database client span must match")
	}
	if id.MatchSpan("query SELECT", "SERVER") {
		t.Error("kind must be part of the match")
	}
	if id.MatchSpan("made-up span", "SERVER") {
		t.Error("an unlisted span name must not match")
	}
}

func TestAServiceWithNoSpanNamesExportsNoSpans(t *testing.T) {
	mcp := load(t).Service("mcp")
	if mcp.ExportsSpans() {
		t.Error("the contract lists no span names for mcp")
	}
	if !load(t).Service("gateway").ExportsSpans() {
		t.Error("the gateway exports spans")
	}
}

func TestUnknownServiceIsNil(t *testing.T) {
	if load(t).Service("nope") != nil {
		t.Error("an unknown service must be nil")
	}
}

func TestResourceAttributesAreTheListedOnes(t *testing.T) {
	for _, name := range load(t).ServiceNames() {
		s := load(t).Service(name)
		if !s.HasResourceAttribute("service.name") || s.HasResourceAttribute("host.name") {
			t.Errorf("%s: resource attributes = %v", name, s.Resource)
		}
	}
}

func TestRedactedSpanNamesMatchByKind(t *testing.T) {
	c := load(t)
	if c.SpanPlaceholder != "[span]" {
		t.Fatalf("placeholder = %q, want [span]", c.SpanPlaceholder)
	}
	id := c.Service("identity")
	if !id.MatchSpan("[span]", "SERVER") || !id.MatchSpan("[span]", "CLIENT") {
		t.Error("the redacted name must match the kinds identity lists")
	}
	if id.MatchSpan("[span]", "PRODUCER") {
		t.Error("the redacted name must still match by kind")
	}
	if c.Service("mcp").MatchSpan("[span]", "SERVER") {
		t.Error("mcp lists no spans, so a redacted span from it is drift")
	}
}
