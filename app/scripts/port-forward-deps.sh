#!/bin/bash
set -e

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl not found in PATH" >&2
  exit 1
fi


echo "Starting port forwards..."

PIDS=()

trap 'kill "${PIDS[@]}" 2>/dev/null' SIGINT EXIT

pf() {
  kubectl port-forward "$@" &
  PIDS+=($!)
}

pf -n cws svc/cms-bff-internal-svc 9090:9090


echo "Port forwards running (PIDs: ${PIDS[*]})"
echo "Press Ctrl+C to stop all..."

wait
