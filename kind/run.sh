#!/usr/bin/env bash
# Copyright 2026 The Sneakers-PAM Authors
# SPDX-License-Identifier: Apache-2.0
#
# The kind drift job's steps, run against the current kubectl context (a
# throwaway kind cluster that already has cert-manager and the service images,
# the way the release repo's install test sets it up).
#
#   collector       deploy the in-cluster collector and wait for it
#   install         run the release repo's install test with every service
#                   exporting to the collector
#   check-real      wait one export interval, copy the recording out and check it
#                   against the contract
#   check-stopped   restart the collector for a clean recording, stop one
#                   service, make its callers use it, then copy the recording
#                   out and check it for the failure signals
#
# Environment:
#   RELEASE_DIR     checkout of sneakers-release at the ref in testdata/telemetry.ref (required for install, check-stopped)
#   NAMESPACE       default sneakers
#   OUT             where recordings go, default ./recordings
#   EXPORT_WAIT     seconds to wait for the SDKs' export interval, default 75
#   STOP_SERVICE    service to stop for check-stopped, default vault
#   LOG_LEVEL       trace prints every command; anything else prints the steps
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/.." && pwd)"
ns="${NAMESPACE:-sneakers}"
out="${OUT:-$repo/recordings}"
export_wait="${EXPORT_WAIT:-75}"
stop_service="${STOP_SERVICE:-vault}"

[ "${LOG_LEVEL:-info}" = "trace" ] && set -x

log() { printf '%s [kind-drift] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

collector_pod() {
  kubectl -n "$ns" get pod -l app.kubernetes.io/name=collector -o jsonpath='{.items[0].metadata.name}'
}

deploy_collector() {
  log "creating namespace $ns and the collector"
  kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$ns" create configmap collector-config --from-file=collector.yaml="$here/collector.yaml" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl -n "$ns" apply -f "$here/collector-deploy.yaml"
  kubectl -n "$ns" rollout status deploy/collector --timeout=180s
  log "collector ready: $(collector_pod)"
}

need_release() {
  if [ -z "${RELEASE_DIR:-}" ] || [ ! -x "$RELEASE_DIR/test/kind/run.sh" ]; then
    log "RELEASE_DIR must be a sneakers-release checkout with test/kind/run.sh"
    exit 2
  fi
}

# copy_out <dir>: copy the collector's /data into <dir> through the sidecar.
copy_out() {
  local dest="$1" pod
  pod="$(collector_pod)"
  rm -rf "$dest"
  mkdir -p "$dest"
  log "copying the recording from $pod to $dest"
  kubectl -n "$ns" cp -c files "$pod:/data/." "$dest"
  ls -l "$dest"
}

# check <dir> <mode>: run the drift check on a recording.
check() {
  log "checking $1 ($2)"
  (cd "$repo" && RECORDING_DIR="$1" RECORDING_MODE="$2" go test ./internal/drift -run 'TestRecording$' -count=1 -v)
}

case "${1:-}" in
  collector)
    deploy_collector
    ;;
  install)
    need_release
    log "installing the release chart with every service exporting to the collector"
    (cd "$RELEASE_DIR" && BASE_VALUES="$here/values.yaml ${BASE_VALUES:-}" NAMESPACE="$ns" test/kind/run.sh)
    ;;
  check-real)
    log "waiting ${export_wait}s for the SDKs to export"
    sleep "$export_wait"
    copy_out "$out/real"
    check "$out/real" real
    ;;
  check-stopped)
    need_release
    # Restart the collector so this recording holds only the stopped run.
    kubectl -n "$ns" rollout restart deploy/collector
    kubectl -n "$ns" rollout status deploy/collector --timeout=180s
    trap 'log "scaling sneakers-$stop_service back up"; kubectl -n "$ns" scale "deploy/sneakers-$stop_service" --replicas=1 || true' EXIT
    log "stopping sneakers-$stop_service"
    kubectl -n "$ns" scale "deploy/sneakers-$stop_service" --replicas=0
    kubectl -n "$ns" wait --for=delete pod -l "app.kubernetes.io/component=$stop_service" --timeout=120s || true
    log "making the callers use it: helm test is expected to fail"
    helm test sneakers -n "$ns" --timeout 5m --logs || log "helm test failed, as expected with $stop_service stopped"
    log "waiting ${export_wait}s for the SDKs to export"
    sleep "$export_wait"
    copy_out "$out/stopped"
    check "$out/stopped" stopped
    ;;
  *)
    sed -n '2,24p' "$0"
    exit 2
    ;;
esac
