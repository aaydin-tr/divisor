#!/usr/bin/env bash
# Packages the chart as the tag pipeline does, then asserts it lints, passes kubeconform -strict
# and defaults to that tag's image. Usage: helm-chart-smoke-test.sh v1.2.3 (helm, kubeconform on PATH)
set -euo pipefail

release_tag="$1"
scripts_dir="$(dirname "$0")"
package_dir="$(mktemp -d)"
trap 'rm -rf "$package_dir"' EXIT

helm lint --strict "$scripts_dir/../../charts/divisor"
chart_package="$(bash "$scripts_dir/helm-package-chart.sh" "$release_tag" "$package_dir")"

helm template divisor "$chart_package" > "$package_dir/rendered.yaml"
kubeconform -strict -summary -kubernetes-version 1.36.0 "$package_dir/rendered.yaml"

expected_image="ghcr.io/aaydin-tr/divisor:$release_tag"
rendered_images="$(sed -n 's/^ *image: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' "$package_dir/rendered.yaml")"
if [ "$rendered_images" != "$expected_image" ]; then
  echo "Deployment image is '$rendered_images', want '$expected_image'" >&2
  exit 1
fi
echo "Chart $(basename "$chart_package" .tgz) renders the Deployment with $expected_image"
