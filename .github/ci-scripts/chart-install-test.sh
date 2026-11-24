#!/usr/bin/env bash
# Install the Helm chart into the current kube context with a LOCALLY built image, then smoke
# test the release: rollout, /health and one OFREP evaluation through a port-forward.
# No image is pulled from a registry for the relay proxy: the image is loaded into the kind
# cluster first.
#
# Usage: chart-install-test.sh <image:tag> [kind-cluster-name]
#   env CHART_DIR   chart directory (default cmd/relayproxy/helm-charts/relay-proxy)
#   env LOCAL_PORT  local port for the port-forward (default 18031)
# Works the same in CI and locally (needs docker, kind, helm, kubectl, curl, jq).
set -euo pipefail

image="${1:?usage: $0 <image:tag> [kind-cluster-name]}"
cluster="${2:-chart-testing}"
chart="${CHART_DIR:-cmd/relayproxy/helm-charts/relay-proxy}"
port="${LOCAL_PORT:-18031}"
release="smoke"
repo="${image%:*}"
tag="${image##*:}"
[[ "$repo" != "$image" ]] || { echo "::error::image must be repo:tag" >&2; exit 2; }

pf_pid=""
cleanup() {
  [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
  helm uninstall "$release" --wait --timeout 60s >/dev/null 2>&1 || true
}
trap cleanup EXIT

kind load docker-image "$image" --name "$cluster"

helm install "$release" "$chart" \
  -f "$chart/ci/smoke-values.yaml" \
  --set "image.repository=$repo" --set "image.tag=$tag" --set image.pullPolicy=Never \
  --wait --timeout 120s || {
    kubectl get pods -o wide || true
    kubectl describe pods || true
    kubectl logs -l app.kubernetes.io/instance="$release" --tail=100 || true
    exit 1
  }

svc="$(kubectl get svc -l app.kubernetes.io/instance="$release" -o jsonpath='{.items[0].metadata.name}')"
kubectl port-forward "svc/$svc" "$port:1031" >/dev/null 2>&1 &
pf_pid=$!
base="http://127.0.0.1:$port"
ready=false
for _ in $(seq 1 30); do
  if ! kill -0 "$pf_pid" 2>/dev/null; then
    echo "::error::kubectl port-forward exited early" >&2
    break
  fi
  if curl -fsS "$base/health" >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  echo "::error::/health not reachable through the port-forward" >&2
  kubectl logs -l app.kubernetes.io/instance="$release" --tail=100 || true
  exit 1
fi

code="$(curl -sS -o /dev/null -w '%{http_code}' "$base/health")"
[[ "$code" == 200 ]] || { echo "::error::/health returned $code" >&2; exit 1; }

body="$(curl -fsS -X POST "$base/ofrep/v1/evaluate/flags/smoke-flag" \
  -H 'Content-Type: application/json' -d '{"context":{"targetingKey":"user-1"}}')"
echo "$body"
[[ "$(jq -r '.value' <<<"$body")" == true ]] || { echo "::error::unexpected OFREP value" >&2; exit 1; }

uid="$(kubectl get pods -l app.kubernetes.io/instance="$release" -o jsonpath='{.items[0].spec.securityContext.runAsUser}')"
[[ "$uid" == 65532 ]] || { echo "::error::pod does not run as uid 65532 (got '$uid')" >&2; exit 1; }
echo "chart install smoke test OK ($image)"
