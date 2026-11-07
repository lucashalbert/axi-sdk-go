#!/usr/bin/env bash
# Fail if any package in this module (including tests) depends on k8s.io.
set -euo pipefail

deps=$(go list -deps -test ./...)
bad=$(grep -E '^k8s\.io/' <<<"$deps" || true)
if [ -n "$bad" ]; then
  echo "axi-sdk-go must not import Kubernetes packages; found:" >&2
  echo "$bad" >&2
  exit 1
fi
echo "ok: no k8s.io dependencies"
