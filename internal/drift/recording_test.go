// Copyright 2026 The Sneakers-PAM Authors
// SPDX-License-Identifier: Apache-2.0

package drift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sneakers-PAM/sneakers-metrics-traces/internal/contract"
)

// TestRecording is the check the kind job runs on what it recorded. It does
// nothing unless RECORDING_DIR is set: the job sets it, with RECORDING_MODE
// "real" for the normal run and "stopped" for the run with a service down.
func TestRecording(t *testing.T) {
	dir := os.Getenv("RECORDING_DIR")
	if dir == "" {
		t.Skip("RECORDING_DIR is not set; the kind job sets it")
	}
	mode := os.Getenv("RECORDING_MODE")
	contractPath := os.Getenv("CONTRACT_FILE")
	if contractPath == "" {
		contractPath = "../../testdata/telemetry.json"
	}
	c, err := contract.Load(contractPath)
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	rec, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("read recording %s: %v", dir, err)
	}
	t.Logf("recording %s (%s) against contract ref %s", dir, mode, c.Ref)

	for _, p := range rec.Drift(c) {
		t.Errorf("drift: %s", p)
	}
	switch mode {
	case "real":
		for _, p := range rec.Missing(c) {
			t.Errorf("missing: %s", p)
		}
	case "stopped":
		for _, p := range rec.FailureSignals() {
			t.Errorf("failure signal: %s", p)
		}
	default:
		t.Fatalf("RECORDING_MODE must be real or stopped, got %q", mode)
	}
}

func TestReadDirReadsTheThreeFilesAndToleratesAbsentOnes(t *testing.T) {
	dir := t.TempDir()
	// Only the metrics file exists.
	line := metric("identity", otelgrpc, "rpc.server.call.duration", map[string]string{"rpc.method": "Get"})
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte(line+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, err := ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	c := load(t)
	if got := rec.Drift(c); len(got) != 0 {
		t.Fatalf("unexpected drift: %v", got)
	}
	missing := strings.Join(rec.Missing(c), "\n")
	if strings.Contains(missing, "identity: no metrics") || !strings.Contains(missing, "identity: no logs") {
		t.Fatalf("metrics were read, logs were absent; Missing said:\n%s", missing)
	}
}

func TestReadDirReportsABrokenFileByName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs.jsonl"), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadDir(dir)
	if err == nil || !strings.Contains(err.Error(), "logs") {
		t.Fatalf("want an error naming logs, got %v", err)
	}
}
