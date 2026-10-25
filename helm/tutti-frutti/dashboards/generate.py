#!/usr/bin/env python3
"""Generate the Grafana dashboard of the Tutti Frutti chart.

    python3 generate.py            write tutti-frutti.json next to this script
    python3 generate.py --check    exit 1 if the committed JSON differs from the
                                   generator output (CI drift check)
    python3 generate.py --stdout   print the JSON instead of writing it

The output is deterministic (no timestamps, ids assigned in layout order) so a
diff of tutti-frutti.json only ever shows real changes. Edit THIS file, never
the JSON. Standard library only.

Metric names are the ones the server exports (internal/room, internal/ws,
internal/httpapi metrics); kube-state-metrics and cAdvisor series cover the
container. test_generate.py greps the Go sources and fails when a panel reads a
metric the server does not register. Every PromQL expression here is meant to
be run against a live Prometheus before it is committed (docs/DESIGN.md,
"Observability (Phase 5)").
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
OUTPUT = HERE / "tutti-frutti.json"

UID = "tutti-frutti"
TITLE = "Tutti Frutti"

# Dashboard variables. `namespace` and `job` default to what the chart deploys
# (release namespace, server Service name); they are query variables, so a
# second install shows up in the dropdowns without editing anything.
DEFAULT_NAMESPACE = "tutti-frutti"
DEFAULT_JOB = "tutti-frutti-server"

DS = {"type": "prometheus", "uid": "${DS_PROMETHEUS}"}

# Label matchers.
M = 'namespace="$namespace", job="$job"'
# kube-state-metrics / cAdvisor series carry no `job` of ours: select the
# server pods by the Deployment name, which equals the Service name (= job).
KS = 'namespace="$namespace", pod=~"$job-.+", container="server"'
KS_POD = 'namespace="$namespace", pod=~"$job-.+"'
# Probes and scrapes are not user traffic.
NOT_PROBES = 'route!~"GET /(healthz|readyz|metrics)"'
RI = "$__rate_interval"
# Unix time of the later of the last successful snapshot write and the start of
# the process (the first is 0 until the process has saved once): the baseline of
# "time since the last save", as the TuttiFruttiSnapshotStale alert counts it.
LAST_SAVE = (
    '{__name__=~"tuttifrutti_snapshot_last_success_timestamp_seconds|process_start_time_seconds", ' + M + "}"
)

GREEN, YELLOW, ORANGE, RED, BLUE, PURPLE = (
    "green",
    "yellow",
    "orange",
    "red",
    "blue",
    "purple",
)

# WebSocket close codes: what the server emits (internal/protocol) plus what
# browsers send. Used for readable legends.
CLOSE_CODES = {
    "1000": "1000 normal",
    "1001": "1001 going away (tab closed)",
    "1006": "1006 abnormal (network drop)",
    "1008": "1008 policy (rate limit, queue overflow, join timeout)",
    "1009": "1009 message too big",
    "1011": "1011 internal error",
    "1012": "1012 server restarting (deploy)",
    "4001": "4001 replaced by a newer connection",
    "4002": "4002 kicked",
    "4003": "4003 unsupported version",
    "4004": "4004 room closed",
    "4005": "4005 left the room",
    "other": "other",
}


class Layout:
    """Left-to-right, top-to-bottom placement on the 24-column grid."""

    def __init__(self) -> None:
        self.panels: list[dict] = []
        self.x = 0
        self.y = 0
        self.row_h = 0
        self.next_id = 1

    def _id(self) -> int:
        i = self.next_id
        self.next_id += 1
        return i

    def row(self, title: str) -> None:
        self._newline()
        self.panels.append(
            {
                "collapsed": False,
                "gridPos": {"h": 1, "w": 24, "x": 0, "y": self.y},
                "id": self._id(),
                "panels": [],
                "title": title,
                "type": "row",
            }
        )
        self.y += 1

    def _newline(self) -> None:
        if self.x:
            self.y += self.row_h
            self.x = 0
            self.row_h = 0

    def add(self, panel: dict, w: int, h: int) -> None:
        if self.x + w > 24:
            self._newline()
        panel["gridPos"] = {"h": h, "w": w, "x": self.x, "y": self.y}
        panel["id"] = self._id()
        self.panels.append(panel)
        self.x += w
        self.row_h = max(self.row_h, h)
        if self.x >= 24:
            self._newline()


def target(expr: str, legend: str = "", ref: str = "A", instant: bool = False) -> dict:
    t = {
        "datasource": DS,
        "editorMode": "code",
        "expr": expr,
        "instant": instant,
        "legendFormat": legend,
        "range": not instant,
        "refId": ref,
    }
    if legend == "":
        t["legendFormat"] = "__auto"
    return t


def thresholds(*steps: tuple[str, float | None]) -> dict:
    return {
        "mode": "absolute",
        "steps": [{"color": c, "value": v} for c, v in steps],
    }


def stat(
    title: str,
    description: str,
    expr: str,
    unit: str = "short",
    *,
    decimals: int | None = None,
    steps: tuple[tuple[str, float | None], ...] = ((BLUE, None),),
    mappings: list | None = None,
    color_mode: str = "value",
    graph: bool = True,
    instant: bool = False,
    no_value: str | None = None,
    calc: str = "lastNotNull",
    legend: str = "",
) -> dict:
    defaults: dict = {
        "color": {"mode": "thresholds"},
        "mappings": mappings or [],
        "thresholds": thresholds(*steps),
        "unit": unit,
    }
    if decimals is not None:
        defaults["decimals"] = decimals
    if no_value is not None:
        defaults["noValue"] = no_value
    return {
        "datasource": DS,
        "description": description,
        "fieldConfig": {"defaults": defaults, "overrides": []},
        "options": {
            "colorMode": color_mode,
            "graphMode": "area" if graph and not instant else "none",
            "justifyMode": "auto",
            "orientation": "auto",
            "reduceOptions": {"calcs": [calc], "fields": "", "values": False},
            "showPercentChange": False,
            "textMode": "auto",
            "wideLayout": True,
        },
        "targets": [target(expr, legend, instant=instant)],
        "title": title,
        "type": "stat",
    }


def timeseries(
    title: str,
    description: str,
    targets: list[tuple[str, str]],
    unit: str = "short",
    *,
    decimals: int | None = None,
    stack: bool = False,
    bars: bool = False,
    fill: int = 12,
    min_: float | None = 0,
    max_: float | None = None,
    steps: tuple[tuple[str, float | None], ...] | None = None,
    overrides: list | None = None,
    legend: str = "list",
    legend_calcs: tuple[str, ...] = (),
    color: str | None = None,
    no_value: str | None = None,
    span_nulls: bool = False,
) -> dict:
    custom: dict = {
        "axisBorderShow": False,
        "axisCenteredZero": False,
        "axisColorMode": "text",
        "axisLabel": "",
        "axisPlacement": "auto",
        "barAlignment": 0,
        "drawStyle": "bars" if bars else "line",
        "fillOpacity": 60 if bars else fill,
        "gradientMode": "none",
        "hideFrom": {"legend": False, "tooltip": False, "viz": False},
        "insertNulls": False,
        "lineInterpolation": "linear",
        "lineWidth": 1,
        "pointSize": 4,
        "scaleDistribution": {"type": "linear"},
        "showPoints": "never",
        "spanNulls": span_nulls,
        "stacking": {"group": "A", "mode": "normal" if stack else "none"},
        "thresholdsStyle": {"mode": "dashed" if steps else "off"},
    }
    defaults: dict = {
        "color": {"mode": "fixed", "fixedColor": color} if color else {"mode": "palette-classic"},
        "custom": custom,
        "mappings": [],
        "thresholds": thresholds(*(steps or ((GREEN, None),))),
        "unit": unit,
    }
    if min_ is not None:
        defaults["min"] = min_
    if max_ is not None:
        defaults["max"] = max_
    if decimals is not None:
        defaults["decimals"] = decimals
    if no_value is not None:
        defaults["noValue"] = no_value
    legend_opts = {
        "calcs": list(legend_calcs),
        "displayMode": "table" if legend == "table" else "list",
        "placement": "right" if legend == "right" else "bottom",
        "showLegend": legend != "hidden",
    }
    return {
        "datasource": DS,
        "description": description,
        "fieldConfig": {"defaults": defaults, "overrides": overrides or []},
        "options": {
            "legend": legend_opts,
            "tooltip": {"mode": "multi", "sort": "desc"},
        },
        "targets": [target(e, lg, chr(ord("A") + i)) for i, (e, lg) in enumerate(targets)],
        "title": title,
        "type": "timeseries",
    }


def state_timeline(title: str, description: str, expr: str, legend: str, mappings: list) -> dict:
    return {
        "datasource": DS,
        "description": description,
        "fieldConfig": {
            "defaults": {
                "color": {"mode": "thresholds"},
                "custom": {"fillOpacity": 70, "lineWidth": 0, "hideFrom": {"legend": False, "tooltip": False, "viz": False}},
                "mappings": mappings,
                "thresholds": thresholds((GREEN, None)),
            },
            "overrides": [],
        },
        "options": {
            "alignValue": "left",
            "legend": {"displayMode": "list", "placement": "bottom", "showLegend": False},
            "mergeValues": True,
            "rowHeight": 0.8,
            "showValue": "auto",
            "tooltip": {"mode": "single", "sort": "none"},
        },
        "targets": [target(expr, legend)],
        "title": title,
        "type": "state-timeline",
    }


def color_override(matcher: str, options: str, color: str) -> dict:
    return {
        "matcher": {"id": matcher, "options": options},
        "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": color}}],
    }


def rename_override(name: str, display: str) -> dict:
    return {
        "matcher": {"id": "byName", "options": name},
        "properties": [{"id": "displayName", "value": display}],
    }


def http_code_overrides() -> list:
    return [
        color_override("byRegexp", "/^2../", GREEN),
        color_override("byRegexp", "/^101$/", PURPLE),
        color_override("byRegexp", "/^3../", BLUE),
        color_override("byRegexp", "/^4../", ORANGE),
        color_override("byRegexp", "/^5../", RED),
    ]


def up_mappings(up: str, down: str) -> list:
    return [
        {
            "type": "value",
            "options": {
                "0": {"color": RED, "index": 1, "text": down},
                "1": {"color": GREEN, "index": 0, "text": up},
            },
        }
    ]


def build_panels() -> list[dict]:
    lo = Layout()

    # ------------------------------------------------------------------ Overview
    lo.row("Overview")
    lo.add(
        stat(
            "Server",
            "UP when Prometheus can scrape the server /metrics endpoint. DOWN also when the target is missing altogether. "
            "Check: kubectl -n $namespace get pods; kubectl -n $namespace logs deploy/$job --tail=100.",
            f"max(up{{{M}}}) or vector(0)",
            mappings=up_mappings("UP", "DOWN"),
            color_mode="background",
            graph=False,
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Uptime",
            "Time since the server process started. Drops to zero on every deploy or crash.",
            f"max(time() - process_start_time_seconds{{{M}}})",
            "s",
            steps=((ORANGE, None), (GREEN, 300)),
            graph=False,
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Active rooms",
            "Live rooms held in memory (tuttifrutti_rooms). The chart caps them at server.maxRooms.",
            f"sum(tuttifrutti_rooms{{{M}}})",
            decimals=0,
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Connected players",
            "Clients attached to a room (joined), tuttifrutti_connections.",
            f"sum(tuttifrutti_connections{{{M}}})",
            decimals=0,
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Open WebSockets",
            "Live WebSocket connections, joined or not (tuttifrutti_ws_connections_open). The server refuses upgrades "
            "above 2000 (503 server_busy): orange at 1500, red at 1900.",
            f"sum(tuttifrutti_ws_connections_open{{{M}}})",
            decimals=0,
            steps=((BLUE, None), (ORANGE, 1500), (RED, 1900)),
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Rooms created / min",
            "Successful POST /api/rooms (HTTP 201) per minute. Rooms are only created over REST.",
            f'sum(rate(tuttifrutti_http_requests_total{{{M}, route="POST /api/rooms", code="201"}}[{RI}])) * 60',
            decimals=2,
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Games started (1 h)",
            "Games the host started in the last hour (tuttifrutti_games_started_total; a rematch starts another game and counts again). "
            "The Gameplay row shows how they ended.",
            f"sum(increase(tuttifrutti_games_started_total{{{M}}}[1h]))",
            decimals=0,
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Container restarts (24 h)",
            "Restarts of the server container (crash, OOM kill, failed probe) in 24 h. A normal deploy replaces the pod and is NOT counted. "
            "Check: kubectl -n $namespace describe pod -l app.kubernetes.io/component=server (Last State).",
            f"sum(increase(kube_pod_container_status_restarts_total{{{KS}}}[24h]))",
            decimals=0,
            steps=((GREEN, None), (ORANGE, 1), (RED, 3)),
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        timeseries(
            "Rooms, connected players and open sockets",
            "Capacity view over time. Sockets above players = connections that have not joined a room yet (join deadline 10 s) "
            "or are being replaced. Vertical markers are server (re)starts: deploys and crashes.",
            [
                (f"sum(tuttifrutti_rooms{{{M}}})", "rooms"),
                (f"sum(tuttifrutti_connections{{{M}}})", "connected players"),
                (f"sum(tuttifrutti_ws_connections_open{{{M}}})", "open sockets"),
            ],
            decimals=0,
        ),
        24,
        7,
    )

    # ------------------------------------------------------------------ Gameplay
    lo.row("Gameplay")
    lo.add(
        stat(
            "Games finished in range",
            "Games that reached the final phase in the selected range (tuttifrutti_games_finished_total), by how they ended: "
            "completed = every configured round was played, ended_by_host = the host ended it early, "
            "letters_exhausted = no letter was left. A game abandoned in the middle (everybody left, idle room) is in none of them.",
            f"sum by (reason) (increase(tuttifrutti_games_finished_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
            graph=False,
            legend="{{reason}}",
        ),
        4,
        4,
    )
    lo.add(
        stat(
            "Rounds in range",
            "Rounds started in the selected time range (tuttifrutti_rounds_total).",
            f"sum(increase(tuttifrutti_rounds_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Basta calls in range",
            "Basta calls in the selected time range (tuttifrutti_basta_total). A vote to stop that passes counts as a Basta.",
            f"sum(increase(tuttifrutti_basta_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Time-ups in range",
            "Rounds that ended because the time limit expired (tuttifrutti_time_ups_total) in the selected range. "
            "Only games with a time limit have them.",
            f"sum(increase(tuttifrutti_time_ups_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Rounds ended by Basta",
            "Basta calls / (Basta calls + time-ups) in the selected range: of the rounds that were played to the end, how many "
            "a player closed by calling Basta (or by a vote to stop that passed) instead of waiting for the clock. "
            "A round that was abandoned counts in neither.",
            f"(sum(increase(tuttifrutti_basta_total{{{M}}}[$__range])) or vector(0)) / "
            f"((sum(increase(tuttifrutti_basta_total{{{M}}}[$__range])) + sum(increase(tuttifrutti_time_ups_total{{{M}}}[$__range]))) > 0)",
            "percentunit",
            decimals=0,
            instant=True,
            no_value="n/a",
        ),
        4,
        4,
    )
    lo.add(
        stat(
            "Letter auto-stops in range",
            "Letters the server picked because the chooser did not tap STOP within 10 s (tuttifrutti_letter_auto_stops_total). "
            "Many of them mean players walk away from the phone or the chooser's connection is bad.",
            f"sum(increase(tuttifrutti_letter_auto_stops_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
        ),
        3,
        4,
    )
    lo.add(
        stat(
            "Rejected commands",
            "Share of client commands answered with an error in the selected range (any result other than ok). "
            "Orange above 5 %, red above 20 %: usually a client bug or someone poking the API.",
            f'(sum(increase(tuttifrutti_commands_total{{{M}, result!="ok"}}[$__range])) or vector(0)) / '
            f"(sum(increase(tuttifrutti_commands_total{{{M}}}[$__range])) > 0)",
            "percentunit",
            decimals=1,
            steps=((GREEN, None), (ORANGE, 0.05), (RED, 0.2)),
            instant=True,
            no_value="n/a",
        ),
        4,
        4,
    )
    lo.add(
        timeseries(
            "Rounds started, Basta calls and time-ups (per min)",
            "Rounds started (tuttifrutti_rounds_total) against the two ways a played round ends: a Basta call "
            "(tuttifrutti_basta_total, a passed vote to stop included) or the time limit (tuttifrutti_time_ups_total). "
            "Started minus the other two is rounds still running or abandoned.",
            [
                (f"sum(rate(tuttifrutti_rounds_total{{{M}}}[{RI}])) * 60", "rounds started"),
                (f"sum(rate(tuttifrutti_basta_total{{{M}}}[{RI}])) * 60", "Basta calls"),
                (f"sum(rate(tuttifrutti_time_ups_total{{{M}}}[{RI}])) * 60", "time-ups"),
            ],
            bars=True,
            decimals=2,
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Games started and finished (per h)",
            "Games started (tuttifrutti_games_started_total) and finished by reason (tuttifrutti_games_finished_total). "
            "The difference is games in progress or abandoned.",
            [
                (f"sum(rate(tuttifrutti_games_started_total{{{M}}}[{RI}])) * 3600", "started"),
                (f"sum by (reason) (rate(tuttifrutti_games_finished_total{{{M}}}[{RI}])) * 3600", "finished: {{reason}}"),
            ],
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "started", BLUE),
                color_override("byName", "finished: completed", GREEN),
                color_override("byName", "finished: ended_by_host", ORANGE),
                color_override("byName", "finished: letters_exhausted", PURPLE),
            ],
            legend="table",
            legend_calcs=("sum",),
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Letter auto-stops (per min)",
            "Letters the server picked because the chooser did not tap within 10 s (tuttifrutti_letter_auto_stops_total). "
            "Empty means every chooser tapped.",
            [(f"sum(rate(tuttifrutti_letter_auto_stops_total{{{M}}}[{RI}])) * 60", "auto-stops")],
            bars=True,
            decimals=2,
            color=ORANGE,
            no_value="none",
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Commands by type (per min, accepted)",
            "Accepted client commands per type (tuttifrutti_commands_total{result=\"ok\"}): what players actually do.",
            [(f'sum by (type) (rate(tuttifrutti_commands_total{{{M}, result="ok"}}[{RI}])) * 60', "{{type}}")],
            stack=True,
            decimals=1,
            legend="table",
            legend_calcs=("mean", "max"),
        ),
        12,
        8,
    )
    lo.add(
        timeseries(
            "Rejected commands by result (per min)",
            "Commands the room refused, by error code: wrong_phase, not_host, invalid_*, rate_limited, server_busy ... "
            "Empty means every command was accepted.",
            [(f'sum by (result) (rate(tuttifrutti_commands_total{{{M}, result!="ok"}}[{RI}])) * 60', "{{result}}")],
            stack=True,
            bars=True,
            decimals=2,
            legend="table",
            legend_calcs=("sum",),
        ),
        12,
        8,
    )
    lo.add(
        timeseries(
            "Rejected-command ratio",
            "Rejected / all commands. Dashed lines: 5 % (orange) and 20 % (red).",
            [
                (
                    f'(sum(rate(tuttifrutti_commands_total{{{M}, result!="ok"}}[{RI}])) or vector(0)) / '
                    f"(sum(rate(tuttifrutti_commands_total{{{M}}}[{RI}])) > 0)",
                    "rejected / total",
                )
            ],
            "percentunit",
            steps=((GREEN, None), (ORANGE, 0.05), (RED, 0.2)),
            max_=1,
            decimals=1,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Rejected-command ratio by type",
            "Rejected / all commands of the same type: shows which command misbehaves.",
            [
                (
                    f'sum by (type) (rate(tuttifrutti_commands_total{{{M}, result!="ok"}}[{RI}])) / '
                    f'(sum by (type) (rate(tuttifrutti_commands_total{{{M}}}[{RI}])) > 0)',
                    "{{type}}",
                )
            ],
            "percentunit",
            max_=1,
            decimals=1,
            legend="table",
            legend_calcs=("max",),
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Votes to stop by result (per h)",
            "Votes to stop a round (tuttifrutti_stop_votes_total, only in lobbies that enabled them): asked = opened (or passed at once "
            "when nobody else was left to ask), then how each ended: passed (the round stopped, counted as a Basta too), declined, "
            "timeout (15 s without agreement) or ended (the round moved on, or the asker left). Empty means nobody used the vote.",
            [(f"sum by (result) (rate(tuttifrutti_stop_votes_total{{{M}}}[{RI}])) * 3600", "{{result}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "asked", BLUE),
                color_override("byName", "passed", GREEN),
                color_override("byName", "declined", ORANGE),
                color_override("byName", "timeout", YELLOW),
                color_override("byName", "ended", PURPLE),
            ],
            legend="table",
            legend_calcs=("sum",),
            no_value="no votes yet",
        ),
        8,
        7,
    )

    # AI judge (part of the gameplay row: it is a review feature)
    lo.add(
        timeseries(
            "AI judge requests by outcome (per h)",
            "Categories the host asked the AI judge about (tuttifrutti_judge_requests_total, only when the server has Cloudflare "
            "credentials): ok = answered, cache_hit = answered from the cache without a call, busy = out of daily budget, rate limit "
            "or refused by Cloudflare, error = the call failed or the reply was unusable. Empty means nobody used it.",
            [(f"sum by (outcome) (rate(tuttifrutti_judge_requests_total{{{M}}}[{RI}])) * 3600", "{{outcome}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "ok", GREEN),
                color_override("byName", "cache_hit", BLUE),
                color_override("byName", "busy", YELLOW),
                color_override("byName", "error", RED),
            ],
            legend="table",
            legend_calcs=("sum",),
            no_value="no judging yet",
        ),
        12,
        7,
    )
    lo.add(
        timeseries(
            "AI judge Workers AI Neurons used today",
            "Neurons spent today (UTC) by this process (tuttifrutti_judge_neurons_used). The Free plan allows 10000 a day and "
            "Cloudflare refuses calls after that; the server stops asking at JUDGE_DAILY_NEURONS (default 9000). It restarts from 0 "
            "when the server restarts.",
            [(f"max(tuttifrutti_judge_neurons_used{{{M}}})", "Neurons")],
            decimals=0,
            max_=10000,
            no_value="judge off",
        ),
        12,
        7,
    )

    # --------------------------------------------------------------- Connections
    lo.row("Connections")
    lo.add(
        timeseries(
            "WebSocket upgrades by status (per min)",
            "GET /ws responses: 101 = connected. 401 missing guest cookie, 403 bad Origin, 429 rate limited / too many connections, "
            "503 server_busy or shutting down.",
            [(f'sum by (code) (rate(tuttifrutti_http_requests_total{{{M}, route="GET /ws"}}[{RI}])) * 60', "{{code}}")],
            stack=True,
            decimals=1,
            overrides=http_code_overrides(),
            legend="table",
            legend_calcs=("sum",),
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "WebSocket close codes (per min)",
            "Why connections ended (tuttifrutti_ws_closes_total by close code). 1001/1006 are normal browser churn, "
            "1012 is a deploy, 1008 is the server cutting off a misbehaving or too slow client.",
            [(f"sum by (code) (rate(tuttifrutti_ws_closes_total{{{M}}}[{RI}])) * 60", "{{code}}")],
            stack=True,
            decimals=1,
            overrides=[rename_override(code, label) for code, label in CLOSE_CODES.items()],
            legend="table",
            legend_calcs=("sum",),
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Reconnect churn (per min)",
            "Upgrades accepted, connections replaced by a newer one of the same player (4001) and abnormal drops (1006). "
            "A spike right after a deploy is expected; a constant high churn means flaky networks or a proxy cutting idle sockets.",
            [
                (f'sum(rate(tuttifrutti_http_requests_total{{{M}, route="GET /ws", code="101"}}[{RI}])) * 60', "upgrades accepted"),
                (f'sum(rate(tuttifrutti_ws_closes_total{{{M}, code="4001"}}[{RI}])) * 60', "replaced (4001)"),
                (f'sum(rate(tuttifrutti_ws_closes_total{{{M}, code="1006"}}[{RI}])) * 60', "abnormal (1006)"),
            ],
            decimals=1,
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Rate-limited requests (per min)",
            "HTTP 429 by route. The buckets are shared by all clients (Traefik hides client IPs), so any sustained value "
            "means real players are being refused. Empty means healthy.",
            [(f'sum by (route) (rate(tuttifrutti_http_requests_total{{{M}, code="429"}}[{RI}])) * 60', "{{route}}")],
            stack=True,
            bars=True,
            decimals=1,
            color=ORANGE,
            legend="table",
            legend_calcs=("sum",),
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Frames in / out (per s)",
            "WebSocket frames by direction (tuttifrutti_ws_frames_total). Outbound dominates: every command triggers one state frame per client.",
            [(f"sum by (dir) (rate(tuttifrutti_ws_frames_total{{{M}}}[{RI}]))", "{{dir}}")],
            "ops",
            decimals=1,
            overrides=[color_override("byName", "in", BLUE), color_override("byName", "out", GREEN)],
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Policy closes and slow-client drops (per min)",
            "Close code 1008 (tuttifrutti_ws_closes_total): the server cut a client off for exceeding its frame rate, missing the join "
            "deadline, sending malformed frames or overflowing its send queue. The red line is exactly the last cause "
            "(tuttifrutti_ws_send_drops_total): the client could not keep up with 32 queued frames and reconnects for a fresh snapshot. "
            "Occasional drops are phones going through a tunnel; a steady stream means bad networks or a server too slow to write.",
            [
                (f'sum(rate(tuttifrutti_ws_closes_total{{{M}, code="1008"}}[{RI}])) * 60', "policy closes (1008)"),
                (f"sum(rate(tuttifrutti_ws_send_drops_total{{{M}}}[{RI}])) * 60", "slow clients dropped (send queue full)"),
            ],
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "policy closes (1008)", ORANGE),
                color_override("byName", "slow clients dropped (send queue full)", RED),
            ],
        ),
        8,
        7,
    )

    # --------------------------------------------------------------- Persistence
    lo.row("Persistence")
    lo.add(
        stat(
            "Snapshot errors in range",
            "Failed room snapshot writes, deletions and janitor sweeps (tuttifrutti_snapshot_errors_total) in the selected range. "
            "Anything above zero means Postgres refused or timed out: rooms keep running from memory but would be lost on a restart. "
            "Check: kubectl -n $namespace logs deploy/$job | grep -i snapshot.",
            f"sum(increase(tuttifrutti_snapshot_errors_total{{{M}}}[$__range]))",
            decimals=0,
            steps=((GREEN, None), (RED, 1)),
            color_mode="background",
            instant=True,
            no_value="0",
        ),
        4,
        6,
    )
    lo.add(
        stat(
            "Time since last successful save",
            "Seconds since the later of the last successful snapshot write (tuttifrutti_snapshot_last_success_timestamp_seconds, 0 until "
            "the process has saved once) and the start of the process. It grows while nothing changes: a room that is playing saves "
            "every 5 s, an idle one every 90 minutes, so orange (90 min) and red (2 h) are what the TuttiFruttiSnapshotStale alert "
            "treats as broken; shorter gaps only matter while snapshots are waiting (panel Snapshot save queue).",
            f"time() - max({LAST_SAVE})",
            "s",
            steps=((GREEN, None), (ORANGE, 5400), (RED, 7200)),
            color_mode="background",
            graph=False,
            instant=True,
            no_value="n/a",
        ),
        4,
        6,
    )
    lo.add(
        stat(
            "Rooms restored at last start",
            "Rooms rebuilt from their snapshots when the current server process started (tuttifrutti_rooms_restored_total; the counter "
            "only moves during the start). After a deploy this is the number of games that survived it.",
            f"sum(tuttifrutti_rooms_restored_total{{{M}}})",
            decimals=0,
            graph=False,
            instant=True,
            no_value="n/a",
        ),
        4,
        6,
    )
    lo.add(
        stat(
            "Restore duration",
            "How long the startup restore took (tuttifrutti_restore_duration_seconds): loading every snapshot and starting its room. "
            "The startup probe allows 150 s; orange above 30 s, red above 120 s.",
            f"max(tuttifrutti_restore_duration_seconds{{{M}}})",
            "s",
            decimals=3,
            steps=((GREEN, None), (ORANGE, 30), (RED, 120)),
            graph=False,
            instant=True,
            no_value="n/a",
        ),
        4,
        6,
    )
    lo.add(
        stat(
            "Restore errors at last start",
            "Saved rooms the current process could not bring back (tuttifrutti_restore_errors_total): unusable snapshots (corrupt, or "
            "written by an incompatible version; they are logged and deleted) and snapshots beyond server.maxRooms (left in the store). "
            "Check: kubectl -n $namespace logs deploy/$job | grep -i 'snapshot\\|not restoring'.",
            f"sum(tuttifrutti_restore_errors_total{{{M}}})",
            decimals=0,
            steps=((GREEN, None), (RED, 1)),
            color_mode="background",
            graph=False,
            instant=True,
            no_value="n/a",
        ),
        4,
        6,
    )
    lo.add(
        stat(
            "Snapshots deleted in range",
            "Snapshots removed from the store in the selected range (tuttifrutti_snapshots_deleted_total): room_closed = a room that "
            "ended removed its own (normal), janitor = the hourly sweep removed a row nobody refreshed for 6 h (should stay at 0: live "
            "rooms refresh theirs every 90 minutes at the latest).",
            f"sum by (source) (increase(tuttifrutti_snapshots_deleted_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
            graph=False,
            legend="{{source}}",
        ),
        4,
        6,
    )
    lo.add(
        timeseries(
            "Snapshot saves by result (per min)",
            "Room snapshot write attempts (tuttifrutti_snapshot_saves_total): ok, or error = encoding or writing failed (a failed write is "
            "retried with backoff, so a database outage shows as a steady stream of errors). Playing rooms save every 5 s, so ok is "
            "roughly 12 per playing room per minute.",
            [(f"sum by (result) (rate(tuttifrutti_snapshot_saves_total{{{M}}}[{RI}])) * 60", "{{result}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[color_override("byName", "ok", GREEN), color_override("byName", "error", RED)],
            legend="table",
            legend_calcs=("sum",),
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Snapshot save error ratio",
            "Failed / all snapshot write attempts. The TuttiFruttiSnapshotSaveFailing alert fires above 50 % (at least 6 attempts in "
            "10 minutes) for 10 minutes; the dashed lines are 20 % (orange) and 50 % (red).",
            [
                (
                    f'(sum(rate(tuttifrutti_snapshot_saves_total{{{M}, result="error"}}[{RI}])) or vector(0)) / '
                    f"(sum(rate(tuttifrutti_snapshot_saves_total{{{M}}}[{RI}])) > 0)",
                    "error / attempts",
                )
            ],
            "percentunit",
            steps=((GREEN, None), (ORANGE, 0.2), (RED, 0.5)),
            max_=1,
            decimals=1,
            color=RED,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Snapshot save latency",
            "Duration of one snapshot write to Postgres (tuttifrutti_snapshot_save_duration_seconds), successful or not. A healthy "
            "database answers in a few milliseconds; the saver gives up on a write after 10 s.",
            [
                (
                    f"histogram_quantile({q}, sum by (le) (rate(tuttifrutti_snapshot_save_duration_seconds_bucket{{{M}}}[{RI}])))",
                    f"p{int(q * 100)}",
                )
                for q in (0.5, 0.95, 0.99)
            ],
            "s",
            decimals=3,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Time since the last successful save",
            "The Time since last successful save stat over time: a sawtooth that resets at every save. Idle rooms only save every 90 "
            "minutes, so teeth of up to 90 minutes are normal; the dashed lines are 15 minutes and 2 hours, the thresholds of the "
            "TuttiFruttiSnapshotStale alert (the 15 minute one only counts while snapshots are waiting).",
            [(f"time() - max({LAST_SAVE})", "since last save")],
            "s",
            steps=((GREEN, None), (ORANGE, 900), (RED, 7200)),
            fill=5,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Snapshot save queue (rooms waiting)",
            "Rooms whose newest snapshot is waiting for the store, being written or waiting for a retry (tuttifrutti_snapshot_save_queue). "
            "It is 0 whenever everything is written: a write takes milliseconds, so a value that stays at 1 or more means the store is "
            "failing or hanging (see Snapshot saves by result).",
            [(f"sum(tuttifrutti_snapshot_save_queue{{{M}}})", "rooms waiting")],
            decimals=0,
            color=ORANGE,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Snapshot errors (per min)",
            "Failed snapshot writes, deletions and janitor sweeps per minute (tuttifrutti_snapshot_errors_total; the writes are also in "
            "Snapshot saves by result). A saver retries with backoff (0.2 s to 10 s) so a database outage shows as a steady stream. "
            "Empty means healthy.",
            [(f"sum(rate(tuttifrutti_snapshot_errors_total{{{M}}}[{RI}])) * 60", "snapshot errors")],
            bars=True,
            decimals=2,
            color=RED,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Snapshot deletions by source (per h)",
            "Snapshots deleted per hour (tuttifrutti_snapshots_deleted_total): room_closed = rooms that ended, janitor = stale rows swept "
            "by the hourly janitor (a janitor line means a snapshot nobody refreshed for 6 hours: investigate).",
            [(f"sum by (source) (rate(tuttifrutti_snapshots_deleted_total{{{M}}}[{RI}])) * 3600", "{{source}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[color_override("byName", "room_closed", BLUE), color_override("byName", "janitor", ORANGE)],
            legend="table",
            legend_calcs=("sum",),
        ),
        12,
        6,
    )
    lo.add(
        state_timeline(
            "Server readiness",
            "kube-state-metrics: the server pod Ready condition. /readyz also pings Postgres, so NOT READY with a healthy "
            "process usually means the database is unreachable.",
            f'max(kube_pod_status_ready{{{KS_POD}, condition="true"}})',
            "ready",
            [
                {
                    "type": "value",
                    "options": {
                        "0": {"color": RED, "index": 1, "text": "NOT READY"},
                        "1": {"color": GREEN, "index": 0, "text": "READY"},
                    },
                }
            ],
        ),
        6,
        6,
    )
    lo.add(
        timeseries(
            "/readyz responses (per min)",
            "Kubelet readiness probes (every 5 s): 200 = ready; 503 = shutting down or the database ping failed. "
            "A short 503 burst at every deploy is expected.",
            [(f'sum by (code) (rate(tuttifrutti_http_requests_total{{{M}, route="GET /readyz"}}[{RI}])) * 60', "{{code}}")],
            stack=True,
            decimals=0,
            overrides=http_code_overrides(),
        ),
        6,
        6,
    )
    lo.add(
        timeseries(
            "Container restarts",
            "Server container restarts per interval (kube-state-metrics). Deploys replace the pod and do not count. "
            "Check Last State / reason: kubectl -n $namespace describe pod -l app.kubernetes.io/component=server.",
            [(f"sum(increase(kube_pod_container_status_restarts_total{{{KS}}}[{RI}]))", "restarts")],
            bars=True,
            decimals=0,
            color=RED,
        ),
        12,
        6,
    )
    lo.add(
        timeseries(
            "Uptime (time since last start)",
            "Resets to zero at every deploy or crash; the sawtooth shows when and how often the server restarted.",
            [(f"max(time() - process_start_time_seconds{{{M}}})", "uptime")],
            "s",
            fill=5,
        ),
        12,
        6,
    )

    # ------------------------------------------------------------------ Accounts
    lo.row("Accounts")
    lo.add(
        timeseries(
            "Logins by result (per min)",
            "Google sign-in attempts that reached the callback (tuttifrutti_logins_total): ok, failed (bad state, token exchange or ID token) "
            "or cancelled (the user declined). Empty until the first login (or while Google login is not configured).",
            [(f"sum by (result) (rate(tuttifrutti_logins_total{{{M}}}[{RI}])) * 60", "{{result}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "ok", GREEN),
                color_override("byName", "failed", RED),
                color_override("byName", "cancelled", BLUE),
            ],
            legend="table",
            legend_calcs=("sum",),
            no_value="no logins yet",
        ),
        8,
        7,
    )
    lo.add(
        stat(
            "Logins in range",
            "Logins by result in the selected range.",
            f"sum by (result) (increase(tuttifrutti_logins_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
            graph=False,
            legend="{{result}}",
        ),
        4,
        7,
    )
    lo.add(
        timeseries(
            "Stats recorded by result (per min)",
            "Lifetime-stats writes at game over (tuttifrutti_stats_recorded_total): ok, error = a failed attempt that is retried, "
            "dropped = given up (stats of that game are lost). Failures never block a room. Empty until the first finished game with a logged-in player.",
            [(f"sum by (result) (rate(tuttifrutti_stats_recorded_total{{{M}}}[{RI}])) * 60", "{{result}}")],
            stack=True,
            bars=True,
            decimals=2,
            overrides=[
                color_override("byName", "ok", GREEN),
                color_override("byName", "error", ORANGE),
                color_override("byName", "dropped", RED),
            ],
            legend="table",
            legend_calcs=("sum",),
            no_value="no stats recorded yet",
        ),
        8,
        7,
    )
    lo.add(
        stat(
            "Stats recorded in range",
            "Stats writes by result in the selected range.",
            f"sum by (result) (increase(tuttifrutti_stats_recorded_total{{{M}}}[$__range]))",
            decimals=0,
            instant=True,
            no_value="0",
            graph=False,
            legend="{{result}}",
        ),
        4,
        7,
    )
    lo.add(
        timeseries(
            "Stats backlog (finished games waiting)",
            "Finished games not yet credited to their players' lifetime stats: the recorder queue (tuttifrutti_stats_queue_depth, games "
            "waiting for the database writer) and the rooms' outboxes (tuttifrutti_stats_outbox_pending, games the store has not "
            "confirmed yet, kept in the room's snapshot). Both are 0 when the database answers; a plateau above 0 means stats writes "
            "are failing (see Stats recorded by result and the TuttiFruttiStatsRecordingFailing alert).",
            [
                (f"sum(tuttifrutti_stats_queue_depth{{{M}}})", "recorder queue"),
                (f"sum(tuttifrutti_stats_outbox_pending{{{M}}})", "room outboxes"),
            ],
            decimals=0,
            no_value="0",
        ),
        12,
        6,
    )

    # ------------------------------------------------------------------ HTTP API
    lo.row("HTTP API")
    lo.add(
        timeseries(
            "Requests by route (per s)",
            "REST and WebSocket-upgrade requests by route pattern, without probes and scrapes.",
            [(f"sum by (route) (rate(tuttifrutti_http_requests_total{{{M}, {NOT_PROBES}}}[{RI}]))", "{{route}}")],
            "reqps",
            stack=True,
            decimals=2,
            legend="table",
            legend_calcs=("mean", "max"),
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Requests by status code (per s)",
            "Status classes of all user traffic (101 = WebSocket upgrade).",
            [(f"sum by (code) (rate(tuttifrutti_http_requests_total{{{M}, {NOT_PROBES}}}[{RI}]))", "{{code}}")],
            "reqps",
            stack=True,
            decimals=2,
            overrides=http_code_overrides(),
            legend="table",
            legend_calcs=("mean",),
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "5xx ratio",
            "Server errors / all requests (probes excluded). Dashed line: 5 %. Check: kubectl -n $namespace logs deploy/$job | grep '\"status\":5'.",
            [
                (
                    f'(sum(rate(tuttifrutti_http_requests_total{{{M}, {NOT_PROBES}, code=~"5.."}}[{RI}])) or vector(0)) / '
                    f"(sum(rate(tuttifrutti_http_requests_total{{{M}, {NOT_PROBES}}}[{RI}])) > 0)",
                    "5xx / total",
                )
            ],
            "percentunit",
            steps=((GREEN, None), (RED, 0.05)),
            max_=1,
            decimals=1,
            color=RED,
        ),
        8,
        8,
    )
    lo.add(
        timeseries(
            "Latency (all routes)",
            "Request latency quantiles from tuttifrutti_http_request_duration_seconds (WebSocket upgrades are excluded: their lifetime is not a latency).",
            [
                (
                    f"histogram_quantile({q}, sum by (le) (rate(tuttifrutti_http_request_duration_seconds_bucket{{{M}, {NOT_PROBES}}}[{RI}])))",
                    f"p{int(q * 100)}",
                )
                for q in (0.5, 0.95, 0.99)
            ],
            "s",
            decimals=3,
        ),
        12,
        8,
    )
    lo.add(
        timeseries(
            "p95 latency by route",
            "95th percentile latency per route pattern.",
            [
                (
                    f"histogram_quantile(0.95, sum by (le, route) (rate(tuttifrutti_http_request_duration_seconds_bucket{{{M}, {NOT_PROBES}}}[{RI}])))",
                    "{{route}}",
                )
            ],
            "s",
            decimals=3,
            legend="table",
            legend_calcs=("max",),
        ),
        12,
        8,
    )

    # ---------------------------------------------------------------- Go runtime
    lo.row("Go runtime and container")
    lo.add(
        timeseries(
            "Goroutines",
            "go_goroutines. Expect roughly 2 per open socket and 2 per room plus a small constant (~15); "
            "steady growth with flat traffic is a goroutine leak.",
            [
                (f"sum(go_goroutines{{{M}}})", "goroutines"),
                (f"sum(tuttifrutti_ws_connections_open{{{M}}}) * 2 + sum(tuttifrutti_rooms{{{M}}}) * 2 + 15", "expected (2/socket + 2/room + 15)"),
            ],
            decimals=0,
            overrides=[
                {
                    "matcher": {"id": "byFrameRefID", "options": "B"},
                    "properties": [
                        {"id": "custom.lineStyle", "value": {"dash": [8, 6], "fill": "dash"}},
                        {"id": "custom.fillOpacity", "value": 0},
                        {"id": "color", "value": {"mode": "fixed", "fixedColor": "gray"}},
                    ],
                }
            ],
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Memory",
            "Go heap in use, process RSS and the container working set against its memory limit (the pod is OOM-killed at the limit).",
            [
                (f"sum(go_memstats_heap_inuse_bytes{{{M}}})", "heap in use"),
                (f"sum(process_resident_memory_bytes{{{M}}})", "process RSS"),
                (f"max(container_memory_working_set_bytes{{{KS}}})", "container working set"),
                (f'max(kube_pod_container_resource_limits{{{KS}, resource="memory"}})', "limit"),
            ],
            "bytes",
            decimals=1,
            overrides=[
                {
                    "matcher": {"id": "byName", "options": "limit"},
                    "properties": [
                        {"id": "color", "value": {"mode": "fixed", "fixedColor": RED}},
                        {"id": "custom.lineStyle", "value": {"dash": [8, 6], "fill": "dash"}},
                        {"id": "custom.fillOpacity", "value": 0},
                    ],
                }
            ],
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "CPU",
            "CPU seconds per second used by the process, against the container CPU request.",
            [
                (f"sum(rate(process_cpu_seconds_total{{{M}}}[{RI}]))", "process CPU (cores)"),
                (f'max(kube_pod_container_resource_requests{{{KS}, resource="cpu"}})', "request"),
            ],
            "short",
            decimals=3,
            overrides=[
                {
                    "matcher": {"id": "byName", "options": "request"},
                    "properties": [
                        {"id": "color", "value": {"mode": "fixed", "fixedColor": "gray"}},
                        {"id": "custom.lineStyle", "value": {"dash": [8, 6], "fill": "dash"}},
                        {"id": "custom.fillOpacity", "value": 0},
                    ],
                }
            ],
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "GC",
            "Garbage-collection cycles per second and the mean pause per cycle.",
            [
                (f"sum(rate(go_gc_duration_seconds_count{{{M}}}[{RI}]))", "cycles per s"),
                (
                    f"sum(rate(go_gc_duration_seconds_sum{{{M}}}[{RI}])) / (sum(rate(go_gc_duration_seconds_count{{{M}}}[{RI}])) > 0)",
                    "mean pause (s)",
                ),
            ],
            "short",
            decimals=4,
            overrides=[
                {
                    "matcher": {"id": "byName", "options": "mean pause (s)"},
                    "properties": [
                        {"id": "unit", "value": "s"},
                        {"id": "custom.axisPlacement", "value": "right"},
                    ],
                }
            ],
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Allocation rate",
            "Bytes allocated per second (go_memstats_alloc_bytes_total).",
            [(f"sum(rate(go_memstats_alloc_bytes_total{{{M}}}[{RI}]))", "allocated")],
            "Bps",
            decimals=1,
        ),
        8,
        7,
    )
    lo.add(
        timeseries(
            "Open file descriptors",
            "process_open_fds as a share of the process limit. Each WebSocket uses one.",
            [(f"max(process_open_fds{{{M}}}) / max(process_max_fds{{{M}}})", "open / max")],
            "percentunit",
            decimals=2,
            steps=((GREEN, None), (ORANGE, 0.7), (RED, 0.9)),
            max_=1,
        ),
        8,
        7,
    )
    return lo.panels


def build_templating() -> dict:
    def query_var(name: str, label: str, query: str, default: str) -> dict:
        return {
            "current": {"selected": False, "text": default, "value": default},
            "datasource": DS,
            "definition": query,
            "hide": 0,
            "includeAll": False,
            "label": label,
            "multi": False,
            "name": name,
            "options": [],
            "query": {"qryType": 1, "query": query, "refId": "PrometheusVariableQueryEditor-VariableQuery"},
            "refresh": 1,
            "regex": "",
            "skipUrlSync": False,
            "sort": 1,
            "type": "query",
        }

    return {
        "list": [
            {
                "current": {"selected": False, "text": "Prometheus", "value": "prometheus"},
                "hide": 0,
                "includeAll": False,
                "label": "Data source",
                "multi": False,
                "name": "DS_PROMETHEUS",
                "options": [],
                "query": "prometheus",
                "queryValue": "",
                "refresh": 1,
                "regex": "",
                "skipUrlSync": False,
                "type": "datasource",
            },
            query_var("namespace", "Namespace", "label_values(tuttifrutti_rooms, namespace)", DEFAULT_NAMESPACE),
            query_var("job", "Job", 'label_values(tuttifrutti_rooms{namespace="$namespace"}, job)', DEFAULT_JOB),
        ]
    }


def build_dashboard() -> dict:
    return {
        "annotations": {
            "list": [
                {
                    "builtIn": 1,
                    "datasource": {"type": "grafana", "uid": "-- Grafana --"},
                    "enable": True,
                    "hide": True,
                    "iconColor": "rgba(0, 211, 255, 1)",
                    "name": "Annotations & Alerts",
                    "type": "dashboard",
                },
                {
                    "datasource": DS,
                    "enable": True,
                    "expr": f"max(process_start_time_seconds{{{M}}}) != max(process_start_time_seconds{{{M}}} offset 1m)",
                    "iconColor": "purple",
                    "name": "Server (re)start",
                    "step": "60s",
                    "textFormat": "Tutti Frutti server started (deploy, crash or restart)",
                    "titleFormat": "Server start",
                },
            ]
        },
        "description": (
            "Tutti Frutti server health, gameplay (games, rounds, time-ups), WebSocket connections, persistence (snapshot saves, "
            "restore), accounts and Go runtime. "
            "Generated by helm/tutti-frutti/dashboards/generate.py: do not edit by hand."
        ),
        "editable": True,
        "fiscalYearStartMonth": 0,
        "graphTooltip": 1,
        "id": None,
        "links": [],
        "liveNow": False,
        "panels": build_panels(),
        "refresh": "30s",
        "schemaVersion": 39,
        "tags": ["tutti-frutti", "game", "go"],
        "templating": build_templating(),
        "time": {"from": "now-6h", "to": "now"},
        "timepicker": {"refresh_intervals": ["10s", "30s", "1m", "5m", "15m", "1h"]},
        "timezone": "browser",
        "title": TITLE,
        "uid": UID,
        "version": 1,
        "weekStart": "",
    }


def validate(dash: dict) -> None:
    """Cheap structural checks so a typo fails the generator, not Grafana."""
    ids: set[int] = set()
    occupied: set[tuple[int, int]] = set()
    for p in dash["panels"]:
        assert p["id"] not in ids, f"duplicate panel id {p['id']}"
        ids.add(p["id"])
        gp = p["gridPos"]
        assert 0 <= gp["x"] and gp["x"] + gp["w"] <= 24, f"{p['title']}: outside the grid"
        for cx in range(gp["x"], gp["x"] + gp["w"]):
            for cy in range(gp["y"], gp["y"] + gp["h"]):
                assert (cx, cy) not in occupied, f"{p['title']}: overlaps another panel"
                occupied.add((cx, cy))
        if p["type"] == "row":
            continue
        assert p["title"] and p.get("description"), f"panel {p['id']} needs a title and a description"
        assert p["targets"], f"{p['title']}: no targets"
        refs = [t["refId"] for t in p["targets"]]
        assert len(refs) == len(set(refs)), f"{p['title']}: duplicate refId"
        for t in p["targets"]:
            assert t["expr"].count("(") == t["expr"].count(")"), f"{p['title']}: unbalanced parentheses"
            assert t["expr"].count("{") == t["expr"].count("}"), f"{p['title']}: unbalanced braces"
            assert "[5m]" not in t["expr"], f"{p['title']}: use $__rate_interval, not a fixed range"


def render() -> str:
    dash = build_dashboard()
    validate(dash)
    # indent=1: the ConfigMap is stored twice by `kubectl apply` (the object and its
    # last-applied annotation, capped at 256 KiB), and whitespace is most of the bytes.
    return json.dumps(dash, indent=1, ensure_ascii=False) + "\n"


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--check", action="store_true", help="fail if the committed JSON is out of date")
    parser.add_argument("--stdout", action="store_true", help="print instead of writing the file")
    args = parser.parse_args(argv)

    out = render()
    if args.stdout:
        sys.stdout.write(out)
        return 0
    if args.check:
        current = OUTPUT.read_text(encoding="utf-8") if OUTPUT.exists() else ""
        if current != out:
            print(f"{OUTPUT.name} is out of date: run `python3 {Path(__file__).name}` in {HERE} and commit the result.", file=sys.stderr)
            return 1
        print(f"{OUTPUT.name} is up to date")
        return 0
    OUTPUT.write_text(out, encoding="utf-8")
    print(f"wrote {OUTPUT} ({len(out)} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
