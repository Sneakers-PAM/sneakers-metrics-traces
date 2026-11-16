// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package dashboards

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	contractFile = "../../testdata/telemetry.json"
	refFile      = "../../testdata/telemetry.ref"
)

func loadContract(t *testing.T) *Contract {
	t.Helper()
	c, err := LoadContract(contractFile)
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	return c
}

// dashboard wraps one Prometheus expression in the smallest dashboard JSON.
func dashboard(expr string) []byte {
	return []byte(fmt.Sprintf(`{"panels":[{"title":"t","datasource":{"type":"prometheus","uid":"prometheus"},`+
		`"targets":[{"expr":%q}]}]}`, expr))
}

func TestContractRefMatchesItsRefFile(t *testing.T) {
	raw, err := os.ReadFile(refFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(raw))
	if got := loadContract(t).Ref; got != want {
		t.Fatalf("testdata/telemetry.json is release ref %q but testdata/telemetry.ref says %q", got, want)
	}
}

func TestExportedNamesFollowTheCollector(t *testing.T) {
	c := loadContract(t)
	for _, name := range []string{
		"rpc_server_call_duration_seconds_bucket",
		"rpc_server_call_duration_seconds_count",
		"rpc_server_call_duration_seconds_sum",
		"http_server_request_duration_seconds_bucket",
		"http_server_request_count_total",
		"http_server_request_body_size_bytes_bucket",
	} {
		if !c.HasMetric(name) {
			t.Errorf("metric %q is not derived from the contract", name)
		}
	}
	for _, name := range []string{
		"rpc_server_call_duration",           // no unit suffix, no series suffix
		"http_server_request_count",          // a counter is exported with _total
		"http_server_request_duration_total", // histograms have no _total
	} {
		if c.HasMetric(name) {
			t.Errorf("metric %q must not be derived from the contract", name)
		}
	}
}

func TestCheckAcceptsKnownQueries(t *testing.T) {
	c := loadContract(t)
	for _, expr := range []string{
		`sum by (job) (rate(rpc_server_call_duration_seconds_count[5m]))`,
		`histogram_quantile(0.95, sum by (job, le) (rate(rpc_server_call_duration_seconds_bucket{job=~"$service"}[$__rate_interval])))`,
		`sum by (http_route) (rate(http_server_request_count_total{http_response_status_code!="200"}[5m])) / 2`,
	} {
		if problems := c.Check(dashboard(expr)); len(problems) != 0 {
			t.Errorf("%s: unexpected problems: %v", expr, problems)
		}
	}
}

func TestCheckRejectsAnUnknownMetric(t *testing.T) {
	problems := loadContract(t).Check(dashboard(`rate(rpc_server_call_latency_seconds_count[5m])`))
	if len(problems) != 1 || !strings.Contains(problems[0], "rpc_server_call_latency_seconds_count") {
		t.Fatalf("want one problem naming the metric, got %v", problems)
	}
}

func TestCheckRejectsAnUnknownLabel(t *testing.T) {
	c := loadContract(t)
	for _, expr := range []string{
		`sum by (grpc_method) (rate(rpc_server_call_duration_seconds_count[5m]))`,
		`rate(rpc_server_call_duration_seconds_count{grpc_method="x"}[5m])`,
	} {
		problems := c.Check(dashboard(expr))
		if len(problems) != 1 || !strings.Contains(problems[0], "grpc_method") {
			t.Errorf("%s: want one problem naming the label, got %v", expr, problems)
		}
	}
}

func TestCheckRejectsALabelTheMetricDoesNotCarry(t *testing.T) {
	// rpc_method belongs to the rpc metrics; the http metrics do not carry it.
	problems := loadContract(t).Check(dashboard(`sum by (rpc_method) (rate(http_server_request_count_total[5m]))`))
	if len(problems) != 1 || !strings.Contains(problems[0], "rpc_method") {
		t.Fatalf("want one problem naming the label, got %v", problems)
	}
}

func TestCheckIgnoresNonPrometheusTargets(t *testing.T) {
	loki := []byte(`{"panels":[{"title":"logs","datasource":{"type":"loki","uid":"loki"},` +
		`"targets":[{"expr":"{service_name=~\".+\"} |= \"not_a_metric\""}]}]}`)
	if problems := loadContract(t).Check(loki); len(problems) != 0 {
		t.Fatalf("a Loki query must not be checked as PromQL: %v", problems)
	}
}

func TestCheckWalksNestedPanels(t *testing.T) {
	nested := []byte(`{"panels":[{"type":"row","panels":[{"title":"t",` +
		`"datasource":{"type":"prometheus","uid":"prometheus"},"targets":[{"expr":"no_such_metric"}]}]}]}`)
	if problems := loadContract(t).Check(nested); len(problems) != 1 {
		t.Fatalf("want one problem from the nested panel, got %v", problems)
	}
}

func TestCommittedDashboardsMatchTheContract(t *testing.T) {
	c := loadContract(t)
	files, err := filepath.Glob("../../dashboards/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no dashboards found in dashboards/")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("checking %s", f)
		for _, p := range c.Check(raw) {
			t.Errorf("%s: %s", f, p)
		}
	}
}
