#!/usr/bin/env bash
# Offline checks of the observability files of the chart (no cluster needed):
#
#   * the dashboard JSON is up to date with dashboards/generate.py, valid, and
#     ends up byte for byte in the rendered ConfigMap;
#   * the PrometheusRule renders, passes `promtool check rules`, every alert has
#     severity, summary, description and runbook, and `promtool test rules`
#     (tests/alerts_test.yaml) passes.
#
# Needs: helm, python3, promtool (PROMTOOL=/path/to/promtool, default: PATH).
# Run from anywhere:  helm/tutti-frutti/ci/check-observability.sh
set -euo pipefail

chart="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
promtool="${PROMTOOL:-promtool}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "== dashboard generator: up to date + unit tests"
python3 "$chart/dashboards/generate.py" --check
(cd "$chart/dashboards" && python3 -B -m unittest discover -s . -p 'test_*.py')
python3 -m json.tool "$chart/dashboards/tutti-frutti.json" > /dev/null

echo "== dashboard ConfigMap carries the JSON unchanged"
helm template tutti-frutti "$chart" --namespace tutti-frutti --set dashboard.enabled=true \
  --show-only templates/grafana-dashboard.yaml > "$tmp/dashboard.yaml"
sed -n '/^  tutti-frutti.json: |-$/,$p' "$tmp/dashboard.yaml" | sed '1d; s/^    //' > "$tmp/dashboard-from-configmap.json"
if ! diff -u "$chart/dashboards/tutti-frutti.json" "$tmp/dashboard-from-configmap.json" > "$tmp/dashboard.diff"; then
  head -20 "$tmp/dashboard.diff"
  echo "the ConfigMap does not contain dashboards/tutti-frutti.json verbatim" >&2
  exit 1
fi
grep -q 'grafana_dashboard: "1"' "$tmp/dashboard.yaml"
size=$(wc -c < "$tmp/dashboard.yaml")
echo "ConfigMap manifest: $size bytes"
[ "$size" -lt 524288 ]

echo "== PrometheusRule renders and passes promtool"
helm template tutti-frutti "$chart" --namespace tutti-frutti --set prometheusRule.enabled=true \
  --show-only templates/prometheusrule.yaml > "$tmp/prometheusrule.yaml"
grep -q '^kind: PrometheusRule$' "$tmp/prometheusrule.yaml"
# spec: is exactly the rule-file format of promtool: drop the line and one indent level.
sed -n '/^spec:$/,$p' "$tmp/prometheusrule.yaml" | sed '1d; s/^  //' > "$tmp/rules-full.yaml"
"$promtool" check rules "$tmp/rules-full.yaml"

alerts=$(grep -c '^ *- alert: ' "$tmp/rules-full.yaml")
for field in 'summary:' 'description:' 'runbook:' 'runbook_url:'; do
  n=$(grep -c "^ *$field" "$tmp/rules-full.yaml")
  [ "$n" -eq "$alerts" ] || { echo "$n of $alerts alerts have $field" >&2; exit 1; }
done
n=$(grep -cE '^ *severity: "(warning|critical)"$' "$tmp/rules-full.yaml")
[ "$n" -eq "$alerts" ] || { echo "$n of $alerts alerts have severity warning|critical" >&2; exit 1; }
n=$(grep -cE '^ *for: [0-9]+m$' "$tmp/rules-full.yaml")
[ "$n" -eq "$alerts" ] || { echo "$n of $alerts alerts have a for: duration" >&2; exit 1; }
echo "$alerts alerts, all with severity/summary/description/runbook/runbook_url/for"

echo "== promtool test rules"
# The unit tests compare labels only: drop the annotation bodies (checked above;
# their templates are validated by `promtool check rules`).
awk '/^        annotations:$/ {skip=1; next} skip && /^ {10,}/ {next} {skip=0; print}' "$tmp/rules-full.yaml" > "$tmp/rules.yaml"
grep -q '^        annotations:' "$tmp/rules.yaml" && { echo "annotation stripping failed" >&2; exit 1; }
cp "$chart/tests/alerts_test.yaml" "$tmp/alerts_test.yaml"
(cd "$tmp" && "$promtool" test rules alerts_test.yaml)

echo "observability checks passed"
