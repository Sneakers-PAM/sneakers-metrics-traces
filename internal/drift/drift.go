// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

// Package drift compares a recording of what the Sneakers services sent with
// the telemetry contract. A recording is what a collector's file exporter
// wrote while the kind install test ran: one OTLP JSON request per line, in
// traces.jsonl, metrics.jsonl and logs.jsonl.
package drift

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/Sneakers-PAM/sneakers-metrics-traces/internal/contract"
)

const maxLine = 64 << 20 // one OTLP request can be large

type anyValue struct {
	String *string `json:"stringValue"`
	Int    any     `json:"intValue"`
	Bool   *bool   `json:"boolValue"`
}

func (v anyValue) text() string {
	switch {
	case v.String != nil:
		return *v.String
	case v.Int != nil:
		return fmt.Sprint(v.Int)
	case v.Bool != nil:
		return strconv.FormatBool(*v.Bool)
	}
	return ""
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type resource struct {
	Attributes []keyValue `json:"attributes"`
}

type points struct {
	DataPoints []struct {
		Attributes []keyValue `json:"attributes"`
	} `json:"dataPoints"`
}

type metricsRequest struct {
	ResourceMetrics []struct {
		Resource     resource `json:"resource"`
		ScopeMetrics []struct {
			Scope struct {
				Name string `json:"name"`
			} `json:"scope"`
			Metrics []struct {
				Name                 string  `json:"name"`
				Histogram            *points `json:"histogram"`
				ExponentialHistogram *points `json:"exponentialHistogram"`
				Sum                  *points `json:"sum"`
				Gauge                *points `json:"gauge"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type tracesRequest struct {
	ResourceSpans []struct {
		Resource   resource `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				Name   string `json:"name"`
				Kind   any    `json:"kind"`
				Status struct {
					Code any `json:"code"`
				} `json:"status"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

type logsRequest struct {
	ResourceLogs []struct {
		Resource  resource `json:"resource"`
		ScopeLogs []struct {
			LogRecords []json.RawMessage `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type resourceSeen struct {
	service string
	keys    []string
}

type point struct {
	service, scope, name string
	attrs                []string
	status               string
}

type spanSeen struct {
	service, name, kind string
	isError             bool
}

// Recording is what the services sent, flattened for checking.
type Recording struct {
	resources []resourceSeen
	points    []point
	spans     []spanSeen
	logs      map[string]int
}

func serviceOf(r resource) (string, []string) {
	var service string
	keys := make([]string, 0, len(r.Attributes))
	for _, a := range r.Attributes {
		keys = append(keys, a.Key)
		if a.Key == "service.name" {
			service = a.Value.text()
		}
	}
	return service, keys
}

// isErrorStatus reports whether a span status code is ERROR (2).
func isErrorStatus(code any) bool {
	switch c := code.(type) {
	case float64:
		return c == 2
	case string:
		return c == "STATUS_CODE_ERROR" || c == "Error"
	}
	return false
}

func lines(r io.Reader, each func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	n := 0
	for sc.Scan() {
		n++
		if len(sc.Bytes()) == 0 {
			continue
		}
		if err := each(sc.Bytes()); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	return sc.Err()
}

// Parse reads the three JSON-lines streams of a recording.
func Parse(traces, metrics, logs io.Reader) (*Recording, error) {
	rec := &Recording{logs: map[string]int{}}

	if err := lines(traces, func(b []byte) error {
		var req tracesRequest
		if err := json.Unmarshal(b, &req); err != nil {
			return err
		}
		for _, rs := range req.ResourceSpans {
			svc, keys := serviceOf(rs.Resource)
			rec.resources = append(rec.resources, resourceSeen{svc, keys})
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					rec.spans = append(rec.spans, spanSeen{svc, sp.Name, contract.SpanKindName(sp.Kind), isErrorStatus(sp.Status.Code)})
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("traces: %w", err)
	}

	if err := lines(metrics, func(b []byte) error {
		var req metricsRequest
		if err := json.Unmarshal(b, &req); err != nil {
			return err
		}
		for _, rm := range req.ResourceMetrics {
			svc, keys := serviceOf(rm.Resource)
			rec.resources = append(rec.resources, resourceSeen{svc, keys})
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, p := range []*points{m.Histogram, m.ExponentialHistogram, m.Sum, m.Gauge} {
						if p == nil {
							continue
						}
						for _, dp := range p.DataPoints {
							pt := point{service: svc, scope: sm.Scope.Name, name: m.Name}
							for _, a := range dp.Attributes {
								pt.attrs = append(pt.attrs, a.Key)
								if a.Key == "rpc.response.status_code" {
									pt.status = a.Value.text()
								}
							}
							rec.points = append(rec.points, pt)
						}
					}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}

	if err := lines(logs, func(b []byte) error {
		var req logsRequest
		if err := json.Unmarshal(b, &req); err != nil {
			return err
		}
		for _, rl := range req.ResourceLogs {
			svc, keys := serviceOf(rl.Resource)
			rec.resources = append(rec.resources, resourceSeen{svc, keys})
			for _, sl := range rl.ScopeLogs {
				rec.logs[svc] += len(sl.LogRecords)
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}

	slog.Debug("recording parsed", "resources", len(rec.resources), "metric_points", len(rec.points),
		"spans", len(rec.spans), "services_with_logs", len(rec.logs))
	return rec, nil
}

// ReadDir reads traces.jsonl, metrics.jsonl and logs.jsonl from dir. A file
// that is absent counts as empty: a service that sent nothing is reported by
// Missing, not here.
func ReadDir(dir string) (*Recording, error) {
	open := func(name string) (io.ReadCloser, error) {
		f, err := os.Open(filepath.Join(dir, name)) // #nosec G304 -- dir is the recording folder the caller chose
		if os.IsNotExist(err) {
			slog.Warn("recording file is absent, reading it as empty", "file", name)
			return io.NopCloser(emptyReader{}), nil
		}
		return f, err
	}
	var rs [3]io.ReadCloser
	for i, name := range []string{"traces.jsonl", "metrics.jsonl", "logs.jsonl"} {
		f, err := open(name)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		defer f.Close() //nolint:errcheck // read-only
		rs[i] = f
	}
	return Parse(rs[0], rs[1], rs[2])
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// Drift lists what the recording holds that the contract does not: a service,
// metric, scope, metric attribute, span name or resource attribute that is not
// listed. Any of these is a hard failure.
func (r *Recording) Drift(c *contract.Contract) []string {
	set := map[string]bool{}
	add := func(format string, args ...any) { set[fmt.Sprintf(format, args...)] = true }

	known := func(service string) *contract.Service {
		s := c.Service(service)
		if s == nil {
			add("service %q is not in the contract (ref %s)", service, c.Ref)
		}
		return s
	}

	for _, res := range r.resources {
		s := known(res.service)
		if s == nil {
			continue
		}
		for _, k := range res.keys {
			if !s.HasResourceAttribute(k) {
				add("resource attribute %q is not in the contract for service %q", k, res.service)
			}
		}
	}
	for _, p := range r.points {
		s := known(p.service)
		if s == nil {
			continue
		}
		if !s.HasMetric(p.name, p.scope) {
			add("metric %q (scope %q) is not in the contract for service %q", p.name, p.scope, p.service)
			continue
		}
		for _, a := range p.attrs {
			if !s.HasMetricAttribute(a) {
				add("attribute %q on metric %q is not in the contract for service %q", a, p.name, p.service)
			}
		}
	}
	for _, sp := range r.spans {
		s := known(sp.service)
		if s == nil {
			continue
		}
		if !s.MatchSpan(sp.name, sp.kind) {
			add("span %q (kind %s) is not in the contract for service %q", sp.name, sp.kind, sp.service)
		}
	}
	return sorted(set)
}

// Missing lists what the contract expects from every service that the
// recording does not hold: some metrics, some logs, and, where the contract
// lists span names, some spans. It asks for one of each, not for every metric,
// because most instruments only appear once the matching traffic has run.
func (r *Recording) Missing(c *contract.Contract) []string {
	metricsBy := map[string]int{}
	for _, p := range r.points {
		metricsBy[p.service]++
	}
	spansBy := map[string]int{}
	for _, sp := range r.spans {
		spansBy[sp.service]++
	}
	var out []string
	for _, name := range c.ServiceNames() {
		s := c.Service(name)
		if metricsBy[name] == 0 {
			out = append(out, name+": no metrics")
		}
		if r.logs[name] == 0 {
			out = append(out, name+": no logs")
		}
		if s.ExportsSpans() && spansBy[name] == 0 {
			out = append(out, name+": no traces")
		}
	}
	return out
}

// FailureSignals checks a recording taken with a service stopped: callers must
// have recorded a gRPC client call that did not end OK, and a span in error
// status. It returns what is absent.
func (r *Recording) FailureSignals() []string {
	var out []string
	var clientError bool
	for _, p := range r.points {
		if p.name == "rpc.client.call.duration" && p.status != "" && p.status != "OK" && p.status != "0" {
			clientError = true
			break
		}
	}
	if !clientError {
		out = append(out, "no rpc.client.call.duration point with a status other than OK")
	}
	var errSpan bool
	for _, sp := range r.spans {
		if sp.isError {
			errSpan = true
			break
		}
	}
	if !errSpan {
		out = append(out, "no span in error status")
	}
	return out
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
