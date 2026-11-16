// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package stack

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const composeFile = "../../compose.yaml"

type composeConfig struct {
	Services map[string]struct {
		Image       string            `json:"image"`
		Environment map[string]string `json:"environment"`
		Volumes     []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"volumes"`
	} `json:"services"`
}

// render runs `docker compose config` on file and returns its JSON.
func render(t *testing.T, file string) ([]byte, error) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed; CI runs this check")
	}
	// A Docker CLI without the compose plugin (a bare Homebrew install) uses the standalone binary.
	cmd := exec.Command("docker", "compose", "-f", file, "config", "--format", "json")
	if exec.Command("docker", "compose", "version").Run() != nil {
		cmd = exec.Command("docker-compose", "-f", file, "config", "--format", "json")
	}
	t.Logf("running: %s", cmd.String())
	return cmd.CombinedOutput()
}

func load(t *testing.T) composeConfig {
	t.Helper()
	out, err := render(t, composeFile)
	if err != nil {
		t.Fatalf("docker compose config failed: %v\n%s", err, out)
	}
	var cfg composeConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("parse compose config: %v\n%s", err, out)
	}
	return cfg
}

func TestStackRunsEveryBackend(t *testing.T) {
	cfg := load(t)
	for _, name := range []string{"collector", "tempo", "prometheus", "loki", "grafana"} {
		if _, ok := cfg.Services[name]; !ok {
			t.Errorf("service %q is missing", name)
		}
	}
}

func TestImagesArePinned(t *testing.T) {
	for name, svc := range load(t).Services {
		_, tag, ok := strings.Cut(svc.Image, ":")
		if !ok || tag == "" || tag == "latest" {
			t.Errorf("service %q image %q is not pinned to a version", name, svc.Image)
		}
	}
}

func TestCollectorUsesTheCommittedConfig(t *testing.T) {
	svc := load(t).Services["collector"]
	var mounted bool
	for _, v := range svc.Volumes {
		if strings.HasSuffix(v.Source, "collector/config.yaml") || strings.HasSuffix(v.Source, "/collector") {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("collector does not mount collector/config.yaml: %+v", svc.Volumes)
	}
	for _, key := range []string{"OTLP_TLS_CERT_FILE", "OTLP_TLS_KEY_FILE", "TEMPO_ENDPOINT", "PROMETHEUS_REMOTE_WRITE_URL", "LOKI_ENDPOINT"} {
		if svc.Environment[key] == "" {
			t.Errorf("collector environment %s is not set", key)
		}
	}
}

func TestBrokenStackFails(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(bad, []byte("services:\n  collector:\n    image: [not, a, string]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := render(t, bad)
	if err == nil {
		t.Fatalf("expected a broken stack file to be rejected, got:\n%s", out)
	}
}
