// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

// Package dashboards checks Grafana dashboards against the telemetry
// contract. The contract is docs/telemetry.json from sneakers-release: the
// metrics the box exports, their types, units and attributes. A dashboard
// that queries a metric or label the contract does not list would be empty on
// a real box, so the check fails it before it ships.
package dashboards

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/Sneakers-PAM/sneakers-metrics-traces/internal/contract"
)

// Contract is the part of the telemetry contract the check needs, expanded
// to the names Prometheus holds after the collector's remote write.
type Contract struct {
	// Ref is the sneakers-release ref the contract was read at.
	Ref string

	// series maps an exported Prometheus series name to the labels it carries.
	series map[string]map[string]bool
}

// unitSuffix is how the collector's Prometheus exporter spells a unit.
var unitSuffix = map[string]string{"": "", "s": "_seconds", "By": "_bytes", "ms": "_milliseconds"}

// baseLabels are on every series the collector writes: service.name becomes
// job, and the instrumentation scope becomes otel_scope_name.
var baseLabels = []string{"job", "instance", "otel_scope_name"}

// LoadContract reads the telemetry contract and derives the Prometheus series
// names and labels from it.
func LoadContract(path string) (*Contract, error) {
	doc, err := contract.Load(path)
	if err != nil {
		return nil, err
	}
	c := &Contract{Ref: doc.Ref, series: map[string]map[string]bool{}}
	for _, m := range doc.Metrics {
		suffix, ok := unitSuffix[m.Unit]
		if !ok {
			return nil, fmt.Errorf("metric %s: unit %q has no known Prometheus suffix", m.Name, m.Unit)
		}
		base := strings.ReplaceAll(m.Name, ".", "_") + suffix
		labels := map[string]bool{}
		for _, l := range baseLabels {
			labels[l] = true
		}
		for _, attrs := range m.Services {
			for _, a := range attrs {
				labels[strings.ReplaceAll(a, ".", "_")] = true
			}
		}
		switch m.Type {
		case "histogram":
			for _, s := range []string{"_bucket", "_count", "_sum"} {
				c.add(base+s, labels, s == "_bucket")
			}
		case "counter":
			c.add(base+"_total", labels, false)
		default:
			return nil, fmt.Errorf("metric %s: type %q is not handled", m.Name, m.Type)
		}
	}
	slog.Debug("series derived from the contract", "ref", c.Ref, "series", len(c.series))
	return c, nil
}

// add records a series; a name seen under two scopes keeps the union of labels.
func (c *Contract) add(name string, labels map[string]bool, bucket bool) {
	have := c.series[name]
	if have == nil {
		have = map[string]bool{}
		c.series[name] = have
	}
	for l := range labels {
		have[l] = true
	}
	if bucket {
		have["le"] = true
	}
}

// HasMetric reports whether name is a series the box exports.
func (c *Contract) HasMetric(name string) bool {
	_, ok := c.series[name]
	return ok
}

type dashboardFile struct {
	Panels []panel `json:"panels"`
}

type datasource struct {
	Type string `json:"type"`
}

type panel struct {
	Title      string      `json:"title"`
	Datasource *datasource `json:"datasource"`
	Targets    []struct {
		Expr       string      `json:"expr"`
		Datasource *datasource `json:"datasource"`
	} `json:"targets"`
	Panels []panel `json:"panels"`
}

// Check returns one problem per metric or label a dashboard's Prometheus
// queries use that the contract does not list. Queries to other data sources
// (Loki, Tempo) are not PromQL and are not checked.
func (c *Contract) Check(dashboardJSON []byte) []string {
	var d dashboardFile
	if err := json.Unmarshal(dashboardJSON, &d); err != nil {
		return []string{fmt.Sprintf("dashboard is not valid JSON: %v", err)}
	}
	var problems []string
	var walk func(panels []panel)
	walk = func(panels []panel) {
		for _, p := range panels {
			walk(p.Panels)
			for _, t := range p.Targets {
				ds := t.Datasource
				if ds == nil {
					ds = p.Datasource
				}
				if ds == nil || ds.Type != "prometheus" || t.Expr == "" {
					continue
				}
				slog.Debug("checking query", "panel", p.Title, "expr", t.Expr)
				for _, msg := range c.checkExpr(t.Expr) {
					problems = append(problems, fmt.Sprintf("panel %q: %s", p.Title, msg))
				}
			}
		}
	}
	walk(d.Panels)
	return problems
}

var (
	reString    = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"|`[^`]*`")
	reVariable  = regexp.MustCompile(`\$\{[^}]*\}|\$\w+`)
	reRange     = regexp.MustCompile(`\[[^\]]*\]`)
	reGrouping  = regexp.MustCompile(`\b(?:by|without|on|ignoring|group_left|group_right)\s*\(([^)]*)\)`)
	reSelector  = regexp.MustCompile(`([A-Za-z_:][A-Za-z0-9_:]*)\s*\{([^}]*)\}`)
	reMatcher   = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:=~|!~|!=|=)`)
	reBraces    = regexp.MustCompile(`\{[^}]*\}`)
	reDuration  = regexp.MustCompile(`\boffset\s+\S+|\b\d+(?:[smhdwy]|ms)\b`)
	reNumber    = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b`)
	reIdent     = regexp.MustCompile(`[A-Za-z_:][A-Za-z0-9_:]*(\s*\()?`)
	promKeyword = map[string]bool{
		"and": true, "or": true, "unless": true, "bool": true, "inf": true, "nan": true,
		"by": true, "without": true, "on": true, "ignoring": true, "group_left": true, "group_right": true,
	}
)

// checkExpr checks one PromQL expression. It is a scanner, not a parser: it
// finds the series names and label names and checks them, which is all the
// contract can say about a query.
func (c *Contract) checkExpr(expr string) []string {
	s := reString.ReplaceAllString(expr, `""`)
	s = reVariable.ReplaceAllString(s, " ")
	s = reRange.ReplaceAllString(s, " ")
	s = reDuration.ReplaceAllString(s, " ")

	var groupLabels []string
	for _, m := range reGrouping.FindAllStringSubmatch(s, -1) {
		for _, l := range strings.Split(m[1], ",") {
			if l = strings.TrimSpace(l); l != "" {
				groupLabels = append(groupLabels, l)
			}
		}
	}
	s = reGrouping.ReplaceAllString(s, " ")

	type selector struct{ metric, label string }
	var matchers []selector
	for _, m := range reSelector.FindAllStringSubmatch(s, -1) {
		for _, l := range reMatcher.FindAllStringSubmatch(m[2], -1) {
			matchers = append(matchers, selector{metric: m[1], label: l[1]})
		}
	}
	s = reBraces.ReplaceAllString(s, " ")
	s = reNumber.ReplaceAllString(s, " ")

	var metrics []string
	seen := map[string]bool{}
	for _, m := range reIdent.FindAllStringSubmatch(s, -1) {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[0]), "("))
		if m[1] != "" || promKeyword[name] || seen[name] {
			continue
		}
		seen[name] = true
		metrics = append(metrics, name)
	}

	var problems []string
	known := map[string]bool{}
	for _, name := range metrics {
		if !c.HasMetric(name) {
			problems = append(problems, fmt.Sprintf("metric %q is not in the telemetry contract (ref %s)", name, c.Ref))
			continue
		}
		for l := range c.series[name] {
			known[l] = true
		}
	}
	for _, m := range matchers {
		if labels, ok := c.series[m.metric]; ok && !labels[m.label] {
			problems = append(problems, fmt.Sprintf("label %q is not on metric %q in the contract", m.label, m.metric))
		}
	}
	if len(known) > 0 {
		for _, l := range groupLabels {
			if !known[l] {
				problems = append(problems, fmt.Sprintf("label %q is not on any metric in the query", l))
			}
		}
	}
	sort.Strings(problems)
	return dedupe(problems)
}

func dedupe(in []string) []string {
	var out []string
	for i, p := range in {
		if i == 0 || p != in[i-1] {
			out = append(out, p)
		}
	}
	return out
}
