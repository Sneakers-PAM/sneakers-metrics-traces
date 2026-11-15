// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectorImage is pinned: the validate step must not move with "latest".
const collectorImage = "otel/opentelemetry-collector-contrib:0.163.2"

// sharedTemp makes a temp dir inside the repo's git-ignored .local folder.
// Docker Desktop shares the home folder with its VM but not the system temp
// dir, so a bind mount from t.TempDir() would arrive empty.
func sharedTemp(t *testing.T) string {
	t.Helper()
	base, err := filepath.Abs("../../.local")
	if err != nil {
		t.Fatalf("abs .local: %v", err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", base, err)
	}
	dir, err := os.MkdirTemp(base, "collector-test-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// writeCert writes a throwaway self-signed certificate and key for
// collector.example.org into dir. Nothing here is committed.
func writeCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "collector.example.org"},
		DNSNames:     []string{"collector.example.org"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

// validate runs `otelcol validate` on cfg in the pinned collector image.
func validate(t *testing.T, cfg string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed; CI runs this check")
	}
	certs := sharedTemp(t)
	writeCert(t, certs)
	abs, err := filepath.Abs(cfg)
	if err != nil {
		t.Fatalf("abs %s: %v", cfg, err)
	}
	var out bytes.Buffer
	cmd := exec.Command("docker", "run", "--rm",
		"-v", filepath.Dir(abs)+":/cfg:ro",
		"-v", certs+":/certs:ro",
		"-e", "OTLP_TLS_CERT_FILE=/certs/tls.crt",
		"-e", "OTLP_TLS_KEY_FILE=/certs/tls.key",
		"-e", "TEMPO_ENDPOINT=tempo:4317",
		"-e", "PROMETHEUS_REMOTE_WRITE_URL=http://prometheus:9090/api/v1/write",
		"-e", "LOKI_ENDPOINT=http://loki:3100/otlp",
		collectorImage, "validate", "--config=/cfg/"+filepath.Base(abs))
	cmd.Stdout, cmd.Stderr = &out, &out
	t.Logf("running: %s", cmd.String())
	err = cmd.Run()
	return out.String(), err
}

func TestConfigValidates(t *testing.T) {
	out, err := validate(t, "../../collector/config.yaml")
	if err != nil {
		t.Fatalf("otelcol validate failed: %v\n%s", err, out)
	}
}

func TestBrokenConfigFails(t *testing.T) {
	bad := filepath.Join(sharedTemp(t), "bad.yaml")
	if err := os.WriteFile(bad, []byte("service:\n  pipelines:\n    traces:\n      receivers: [nope]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := validate(t, bad)
	if err == nil {
		t.Fatalf("expected validate to reject a broken config, got:\n%s", out)
	}
	// It must fail on the config itself, not on a mount or image problem.
	if !strings.Contains(out, "service::pipelines::traces") {
		t.Fatalf("validate failed for another reason:\n%s", out)
	}
}
