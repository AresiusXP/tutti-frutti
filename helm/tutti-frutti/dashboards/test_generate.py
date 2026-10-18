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
            "tuttifrutti_snapshot_errors_total",
            "tuttifrutti_ws_closes_total",
            "tuttifrutti_ws_frames_total",
            "tuttifrutti_http_requests_total",
            "tuttifrutti_http_request_duration_seconds_bucket",
            "tuttifrutti_logins_total",
            "go_goroutines",
        ):
            self.assertIn(metric, self.text)

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
