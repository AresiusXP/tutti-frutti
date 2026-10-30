"""Checks of the generated dashboard (run: python3 -m unittest discover -s helm/tutti-frutti/dashboards).

They pin the contract of the shipped dashboard (uid, datasource variable, no
hard-coded namespace, units, rate intervals, size) and that the committed JSON
is exactly what generate.py produces.
"""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path

import generate

HERE = Path(__file__).resolve().parent
CHART = HERE.parent
REPO = HERE.parents[2]
SERVER = REPO / "server" / "internal"
DESIGN = REPO / "docs" / "DESIGN.md"
RULES = CHART / "templates" / "prometheusrule.yaml"

# Series of other exporters the dashboard and the alerts read (kube-state-metrics,
# cAdvisor, the Go and process collectors, Prometheus itself).
FOREIGN = re.compile(r"^(up|go_.*|process_.*|kube_.*|container_.*)$")
HISTOGRAM_SUFFIX = re.compile(r"_(bucket|sum|count)$")

# Server metrics that feed neither a panel nor an alert: diagnostic only, and
# documented as such in the metrics table of docs/DESIGN.md. Keep it empty if you can.
DIAGNOSTIC_ONLY: set[str] = {
    # Counts the users a restore or a deletion left out of the statistics (see the
    # account deletion notes of docs/DESIGN.md); read in the logs and /metrics.
    "tuttifrutti_stats_skipped_users_total",
}


def registered_metrics() -> set[str]:
    """Every tuttifrutti_* metric the server registers (Name: "..." in the non-test Go sources)."""
    names: set[str] = set()
    for path in SERVER.rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        names.update(re.findall(r'Name:\s*"(tuttifrutti_[a-z0-9_]+)"', path.read_text(encoding="utf-8")))
    return names


def base_name(metric: str, registered: set[str]) -> str:
    """tuttifrutti_x_bucket -> tuttifrutti_x when tuttifrutti_x is registered."""
    base = HISTOGRAM_SUFFIX.sub("", metric)
    return base if base in registered and base != metric else metric


def panels(dash):
    return [p for p in dash["panels"] if p["type"] != "row"]


def exprs(dash):
    return [(p["title"], t["expr"]) for p in panels(dash) for t in p["targets"]]


class DashboardTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = (HERE / "tutti-frutti.json").read_text(encoding="utf-8")
        cls.dash = json.loads(cls.text)

    def test_committed_json_is_what_the_generator_produces(self):
        self.assertEqual(self.text, generate.render(), "run `python3 generate.py` in helm/tutti-frutti/dashboards and commit")

    def test_generator_is_deterministic(self):
        self.assertEqual(generate.render(), generate.render())

    def test_identity_and_defaults(self):
        d = self.dash
        self.assertEqual(d["uid"], "tutti-frutti")
        self.assertEqual(d["title"], "Tutti Frutti")
        self.assertIsNone(d["id"], "id must be null so the sidecar/import can assign one")
        self.assertEqual(d["refresh"], "30s")
        self.assertEqual(d["time"], {"from": "now-6h", "to": "now"})
        self.assertGreaterEqual(d["schemaVersion"], 39)
        self.assertIn("tutti-frutti", d["tags"])

    def test_size_stays_well_under_the_kubernetes_limits(self):
        # A ConfigMap is capped at 1 MiB, but client-side apply (ArgoCD) also
        # stores the whole object, JSON-escaped, in the last-applied-configuration
        # annotation, and all annotations together are capped at 256 KiB.
        self.assertLess(len(self.text.encode()), 512 * 1024)
        self.assertLess(len(json.dumps({"data": {"tutti-frutti.json": self.text}})), 192 * 1024)

    def test_variables(self):
        variables = {v["name"]: v for v in self.dash["templating"]["list"]}
        self.assertEqual(set(variables), {"DS_PROMETHEUS", "namespace", "job"})
        self.assertEqual(variables["DS_PROMETHEUS"]["type"], "datasource")
        self.assertEqual(variables["DS_PROMETHEUS"]["query"], "prometheus")
        self.assertEqual(variables["namespace"]["current"]["value"], "tutti-frutti")
        self.assertEqual(variables["job"]["current"]["value"], "tutti-frutti-server")
        for name in ("namespace", "job"):
            self.assertEqual(variables[name]["type"], "query")
            self.assertEqual(variables[name]["datasource"]["uid"], "${DS_PROMETHEUS}")

    def test_every_panel_uses_the_datasource_variable(self):
        for p in panels(self.dash):
            self.assertEqual(p["datasource"], generate.DS, p["title"])
            for t in p["targets"]:
                self.assertEqual(t["datasource"], generate.DS, p["title"])

    def test_queries_are_scoped_by_variables_not_hard_coded(self):
        for title, e in exprs(self.dash):
            self.assertNotRegex(e, r'namespace="(?!\$namespace")', title)
            self.assertNotRegex(e, r'job="(?!\$job")', title)
            self.assertNotIn("tutti-frutti-server", e, title)
            if "tuttifrutti_" in e or e.startswith(("max(up", "max(time()")):
                self.assertIn('namespace="$namespace"', e, title)

    def test_range_windows_are_variables_or_fixed_stat_windows(self):
        allowed = ("$__rate_interval", "$__range", "1h", "24h")
        for title, e in exprs(self.dash):
            for window in re.findall(r"\[([^\]]+)\]", e):
                self.assertIn(window, allowed, f"{title}: [{window}]")
            if "rate(" in e:
                self.assertIn("$__rate_interval", e, title)

    def test_panels_have_description_unit_and_a_known_type(self):
        for p in panels(self.dash):
            self.assertGreater(len(p["description"]), 20, p["title"])
            self.assertIn(p["type"], {"stat", "timeseries", "state-timeline"}, p["title"])
            if p["type"] != "state-timeline":
                self.assertIn("unit", p["fieldConfig"]["defaults"], p["title"])

    def test_rows_present_and_in_order(self):
        rows = [p["title"] for p in self.dash["panels"] if p["type"] == "row"]
        self.assertEqual(
            rows,
            ["Overview", "Gameplay", "Connections", "Persistence", "Accounts", "HTTP API", "Go runtime and container"],
        )

    def test_metrics_the_server_exports_are_covered(self):
        for metric in (
            "tuttifrutti_rooms",
            "tuttifrutti_connections",
            "tuttifrutti_ws_connections_open",
            "tuttifrutti_commands_total",
            "tuttifrutti_rounds_total",
            "tuttifrutti_basta_total",
            "tuttifrutti_stop_votes_total",
            "tuttifrutti_games_started_total",
            "tuttifrutti_games_finished_total",
            "tuttifrutti_time_ups_total",
            "tuttifrutti_letter_auto_stops_total",
            "tuttifrutti_snapshot_errors_total",
            "tuttifrutti_snapshot_saves_total",
            "tuttifrutti_snapshot_save_duration_seconds_bucket",
            "tuttifrutti_snapshot_last_success_timestamp_seconds",
            "tuttifrutti_snapshot_save_queue",
            "tuttifrutti_snapshots_deleted_total",
            "tuttifrutti_rooms_restored_total",
            "tuttifrutti_restore_duration_seconds",
            "tuttifrutti_restore_errors_total",
            "tuttifrutti_ws_closes_total",
            "tuttifrutti_ws_send_drops_total",
            "tuttifrutti_ws_frames_total",
            "tuttifrutti_http_requests_total",
            "tuttifrutti_http_request_duration_seconds_bucket",
            "tuttifrutti_logins_total",
            "tuttifrutti_reports_total",
            "tuttifrutti_reports_open",
            "tuttifrutti_report_oldest_open_age_seconds",
            "tuttifrutti_judge_enabled",
            "tuttifrutti_stats_recorded_total",
            "tuttifrutti_stats_queue_depth",
            "tuttifrutti_stats_outbox_pending",
            "go_goroutines",
        ):
            self.assertIn(metric, self.text)

    def test_no_panel_approximates_a_metric_the_server_now_exports(self):
        # These were proxies before the server counted the real thing.
        for title, e in exprs(self.dash):
            self.assertNotIn('type="start_game"', e, f"{title}: games started come from tuttifrutti_games_started_total")
        by_title = {p["title"]: p for p in panels(self.dash)}
        for title in (
            "Games started (1 h)",
            "Games finished in range",
            "Time-ups in range",
            "Rounds ended by Basta",
            "Letter auto-stops in range",
            "Games started and finished (per h)",
            "Letter auto-stops (per min)",
            "Policy closes and slow-client drops (per min)",
            "Snapshot saves by result (per min)",
            "Snapshot save error ratio",
            "Snapshot save latency",
            "Time since last successful save",
            "Time since the last successful save",
            "Snapshot save queue (rooms waiting)",
            "Rooms restored at last start",
            "Restore duration",
            "Restore errors at last start",
            "Snapshots deleted in range",
            "Snapshot deletions by source (per h)",
            "Stats backlog (finished games waiting)",
            "Votes to stop by result (per h)",
        ):
            self.assertIn(title, by_title)
        self.assertNotIn("no separate time-up metric", self.text)
        self.assertNotIn("no dedicated send-queue-drop metric", self.text)

    def test_rounds_ended_by_basta_uses_basta_and_time_ups(self):
        e = next(p for p in panels(self.dash) if p["title"] == "Rounds ended by Basta")["targets"][0]["expr"]
        self.assertIn("tuttifrutti_basta_total", e)
        self.assertIn("tuttifrutti_time_ups_total", e)
        self.assertNotIn("tuttifrutti_rounds_total", e)

    def test_last_save_panels_use_the_baseline_of_the_stale_alert(self):
        # time since max(last successful save, process start): the timestamp is 0 until the first save.
        baseline = "tuttifrutti_snapshot_last_success_timestamp_seconds|process_start_time_seconds"
        for title in ("Time since last successful save", "Time since the last successful save"):
            e = next(p for p in panels(self.dash) if p["title"] == title)["targets"][0]["expr"]
            self.assertIn(baseline, e, title)
        self.assertIn(baseline, RULES.read_text(encoding="utf-8"))

    def test_stat_panels_have_thresholds(self):
        for p in panels(self.dash):
            if p["type"] == "stat":
                self.assertTrue(p["fieldConfig"]["defaults"]["thresholds"]["steps"], p["title"])

    def test_ratio_panels_start_at_zero(self):
        for p in panels(self.dash):
            d = p["fieldConfig"]["defaults"]
            if p["type"] == "timeseries" and d.get("unit") == "percentunit":
                self.assertEqual(d.get("min"), 0, p["title"])

    def test_ratios_guard_against_an_empty_denominator(self):
        # "x / (y > 0)" drops the point instead of plotting NaN when there is no traffic.
        for title, e in exprs(self.dash):
            if re.search(r"\) / \(", e) or " / (" in e:
                self.assertRegex(e, r"> 0\)", title)


@unittest.skipUnless(SERVER.is_dir(), "the Go sources are not next to the chart (packaged chart?)")
class MetricsExistTest(unittest.TestCase):
    """Dashboard, alerts and docs only talk about metrics the server registers, and vice versa."""

    @classmethod
    def setUpClass(cls):
        cls.registered = registered_metrics()
        cls.dash_text = (HERE / "tutti-frutti.json").read_text(encoding="utf-8")
        cls.dash = json.loads(cls.dash_text)
        cls.rules = RULES.read_text(encoding="utf-8")

    def used_in(self, text: str) -> set[str]:
        return {base_name(m, self.registered) for m in re.findall(r"\b(tuttifrutti_[a-z0-9_]+)", text)}

    def test_the_go_sources_register_what_is_expected(self):
        # A sanity check of the grep itself: it must see the metrics of every package.
        for name in (
            "tuttifrutti_rooms",
            "tuttifrutti_ws_closes_total",
            "tuttifrutti_http_requests_total",
            "tuttifrutti_logins_total",
            "tuttifrutti_snapshot_saves_total",
        ):
            self.assertIn(name, self.registered)

    def test_every_tuttifrutti_series_a_panel_reads_is_registered(self):
        for title, e in exprs(self.dash):
            for metric in re.findall(r"\b(tuttifrutti_[a-z0-9_]+)", e):
                self.assertIn(base_name(metric, self.registered), self.registered, f"{title}: {metric}")

    def test_every_selector_in_a_panel_is_a_registered_or_a_known_foreign_series(self):
        for title, e in exprs(self.dash):
            for name in re.findall(r"\b([a-z_][a-z0-9_]*)\{", e):
                if name.startswith("tuttifrutti_"):
                    self.assertIn(base_name(name, self.registered), self.registered, f"{title}: {name}")
                else:
                    self.assertRegex(name, FOREIGN, f"{title}: {name}")

    def test_every_series_an_alert_reads_is_registered(self):
        used = self.used_in(self.rules)
        self.assertTrue(used)
        for metric in used:
            self.assertIn(metric, self.registered, metric)

    def test_the_panels_named_in_the_runbooks_exist(self):
        titles = [p["title"] for p in panels(self.dash)]
        named = []
        for group in re.findall(r'panels? ((?:"[^"]+"(?:, | and )?)+)', self.rules):
            named += re.findall(r'"([^"]+)"', group)
        self.assertTrue(named)
        for name in named:
            self.assertTrue(any(t.startswith(name) for t in titles), f'runbook names a panel "{name}" that the dashboard does not have')

    def test_every_registered_metric_feeds_a_panel_or_an_alert(self):
        used = self.used_in(self.dash_text) | self.used_in(self.rules)
        for metric in sorted(self.registered - DIAGNOSTIC_ONLY):
            self.assertIn(metric, used, f"{metric} feeds neither a panel nor an alert: use it, or list it in DIAGNOSTIC_ONLY and document it as diagnostic")
        for metric in DIAGNOSTIC_ONLY:
            self.assertIn(metric, self.registered)

    @unittest.skipUnless(DESIGN.is_file(), "docs/DESIGN.md is not next to the chart")
    def test_the_metrics_table_of_the_design_lists_exactly_the_registered_metrics(self):
        text = DESIGN.read_text(encoding="utf-8")
        start = text.index("**Metrics** (server `/metrics`")
        table = text[start : text.index("The dashboard and alerts also read", start)]
        documented = set(re.findall(r"^\| `(tuttifrutti_[a-z0-9_]+)`", table, flags=re.M))
        self.assertEqual(documented, self.registered)
        for metric in DIAGNOSTIC_ONLY:
            row = next(line for line in table.splitlines() if f"`{metric}`" in line)
            self.assertIn("diagnostic", row.lower())

    @unittest.skipUnless(DESIGN.is_file(), "docs/DESIGN.md is not next to the chart")
    def test_the_alerts_table_of_the_design_lists_exactly_the_rendered_alerts(self):
        text = DESIGN.read_text(encoding="utf-8")
        start = text.index("| Alert ")
        table = text[start : text.index("**Extending.**", start)]
        documented = set(re.findall(r"^\| `(TuttiFrutti[A-Za-z]+)`", table, flags=re.M))
        alerts = set(re.findall(r"- alert: (TuttiFrutti[A-Za-z]+)", self.rules))
        self.assertEqual(documented, alerts)


class GeneratorValidationTest(unittest.TestCase):
    def test_validate_rejects_overlapping_panels(self):
        dash = generate.build_dashboard()
        first = [p for p in dash["panels"] if p["type"] != "row"]
        first[1]["gridPos"] = dict(first[0]["gridPos"])
        with self.assertRaises(AssertionError):
            generate.validate(dash)

    def test_validate_rejects_fixed_rate_windows(self):
        dash = generate.build_dashboard()
        p = next(p for p in dash["panels"] if p["type"] == "timeseries")
        p["targets"][0]["expr"] = "sum(rate(x[5m]))"
        with self.assertRaises(AssertionError):
            generate.validate(dash)

    def test_validate_rejects_unbalanced_expressions(self):
        dash = generate.build_dashboard()
        p = next(p for p in dash["panels"] if p["type"] == "timeseries")
        p["targets"][0]["expr"] = 'sum(rate(x{a="b"}[$__rate_interval])'
        with self.assertRaises(AssertionError):
            generate.validate(dash)


if __name__ == "__main__":
    unittest.main()
