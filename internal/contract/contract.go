// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

// Package contract reads the Sneakers telemetry contract: docs/telemetry.json
// from sneakers-release, which lists for each of the nine services the
// metrics, span names and resource attributes it exports. The dashboards check
// and the drift check both read it here, so there is one parser.
package contract

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Metric is one instrument in the contract.
type Metric struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Unit  string `json:"unit"`
	Scope string `json:"scope"`
	// Services maps a service name to the attributes the metric carries there.
	Services map[string][]string `json:"services"`
}

// MetricRef names a metric a service exports, by name and instrumentation scope.
type MetricRef struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

// SpanName is a span name pattern a service may start, with its span kind
// (SERVER, CLIENT or INTERNAL).
type SpanName struct {
	Pattern *regexp.Regexp
	Kind    string
}

// Service is the contract for one service.
type Service struct {
	Name        string
	Resource    []string
	Spans       []SpanName
	Metrics     []MetricRef
	MetricAttrs []string
}

// Contract is the whole contract at one release ref.
type Contract struct {
	// Ref is the sneakers-release ref the contract was read at.
	Ref string
	// Metrics are the instruments, as in docs/telemetry.json.
	Metrics []Metric

	services map[string]*Service
}

type document struct {
	Release struct {
		Ref string `json:"ref"`
	} `json:"release"`
	Metrics  []Metric `json:"metrics"`
	Services map[string]struct {
		Resource []string `json:"resource"`
		Spans    struct {
			Names []struct {
				Pattern string `json:"pattern"`
				Kind    string `json:"kind"`
			} `json:"names"`
		} `json:"spans"`
		Metrics     []MetricRef `json:"metrics"`
		MetricAttrs []string    `json:"metric_attributes"`
	} `json:"services"`
}

// Load reads a contract file.
func Load(path string) (*Contract, error) {
	slog.Debug("loading telemetry contract", "path", path)
	raw, err := os.ReadFile(path) // #nosec G304 -- path is a fixture or a flag the caller chose, not user input
	if err != nil {
		return nil, fmt.Errorf("read contract %s: %w", path, err)
	}
	var d document
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse contract %s: %w", path, err)
	}
	c := &Contract{Ref: d.Release.Ref, Metrics: d.Metrics, services: map[string]*Service{}}
	for name, s := range d.Services {
		svc := &Service{Name: name, Resource: s.Resource, Metrics: s.Metrics, MetricAttrs: s.MetricAttrs}
		for _, n := range s.Spans.Names {
			re, err := regexp.Compile(n.Pattern)
			if err != nil {
				return nil, fmt.Errorf("service %s: span pattern %q: %w", name, n.Pattern, err)
			}
			svc.Spans = append(svc.Spans, SpanName{Pattern: re, Kind: n.Kind})
		}
		c.services[name] = svc
	}
	slog.Debug("telemetry contract loaded", "ref", c.Ref, "services", len(c.services), "metrics", len(c.Metrics))
	return c, nil
}

// ServiceNames lists the services in the contract, sorted.
func (c *Contract) ServiceNames() []string {
	names := make([]string, 0, len(c.services))
	for n := range c.services {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Service returns one service's contract, or nil when the contract has none.
func (c *Contract) Service(name string) *Service { return c.services[name] }

// HasMetric reports whether the service exports a metric of that name under that scope.
func (s *Service) HasMetric(name, scope string) bool {
	for _, m := range s.Metrics {
		if m.Name == name && m.Scope == scope {
			return true
		}
	}
	return false
}

// HasMetricName reports whether the service exports a metric of that name under any scope.
func (s *Service) HasMetricName(name string) bool {
	for _, m := range s.Metrics {
		if m.Name == name {
			return true
		}
	}
	return false
}

// ExportsSpans reports whether the contract lists any span name for the service.
func (s *Service) ExportsSpans() bool { return len(s.Spans) > 0 }

// MatchSpan reports whether a span of that name and kind is in the contract.
func (s *Service) MatchSpan(name, kind string) bool {
	for _, sp := range s.Spans {
		if sp.Kind == kind && sp.Pattern.MatchString(name) {
			return true
		}
	}
	return false
}

// HasResourceAttribute reports whether key is a resource attribute the service may export.
func (s *Service) HasResourceAttribute(key string) bool { return contains(s.Resource, key) }

// HasMetricAttribute reports whether key is an attribute the service's metrics may carry.
func (s *Service) HasMetricAttribute(key string) bool { return contains(s.MetricAttrs, key) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// SpanKindName turns the protobuf span kind number the collector's file
// exporter writes (or its SPAN_KIND_ name) into SERVER, CLIENT and so on.
func SpanKindName(kind any) string {
	switch k := kind.(type) {
	case float64:
		kinds := [...]string{"UNSPECIFIED", "INTERNAL", "SERVER", "CLIENT", "PRODUCER", "CONSUMER"}
		if k >= 0 && int(k) < len(kinds) {
			return kinds[int(k)]
		}
	case string:
		return strings.TrimPrefix(k, "SPAN_KIND_")
	}
	return "UNSPECIFIED"
}
