#!/usr/bin/env bash
# Packages charts/divisor for a release tag: version = tag without "v", appVersion = tag (the image tag).
# Usage: helm-package-chart.sh v1.2.3 <destination dir>; prints the package path.
set -euo pipefail

release_tag="$1"
destination="$2"
chart_version="${release_tag#v}"
chart_dir="$(dirname "$0")/../../charts/divisor"

helm package "$chart_dir" --version "$chart_version" --app-version "$release_tag" --destination "$destination" >&2
echo "$destination/divisor-$chart_version.tgz"
