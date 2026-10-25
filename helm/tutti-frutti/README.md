# tutti-frutti Helm chart

One chart for the whole game: the Go **server** (REST `/api`, WebSocket `/ws`),
the static **web** app (nginx-unprivileged) and the **Ingress**.

```text
oci://ghcr.io/aresiusxp/charts/tutti-frutti
```

The chart `version` and `appVersion` are always the same value (one version for
the whole monorepo) and are bumped by release-please. Both images default to the
`appVersion`, so a chart version always deploys a matching server/web pair.

## Install / upgrade

```bash
helm upgrade --install tutti-frutti oci://ghcr.io/aresiusxp/charts/tutti-frutti \
  --version <X.Y.Z> -n tutti-frutti --create-namespace
```

In the homelab ArgoCD does this: Renovate bumps the chart version in
`argocd-apps/bootstrap/values.yaml` and ArgoCD syncs. Local checks:

```bash
helm lint helm/tutti-frutti
helm template t helm/tutti-frutti
helm template t helm/tutti-frutti --set serviceMonitor.enabled=true
```

Notes:

* The **server always runs exactly one replica with `strategy: Recreate`**.
  Game state lives in memory, so there is deliberately no replica value. Do not
  scale it out without redesigning state. During a rollout clients reconnect.
* The **web** deployment is a regular rolling update (`web.replicaCount`).
* Pods run non-root with a read-only root filesystem, all capabilities dropped
  and the `RuntimeDefault` seccomp profile. The web pod mounts `emptyDir`s on
  `/tmp`, `/var/cache/nginx` and `/var/run` for nginx.
* If your images use different UIDs, override
  `server.podSecurityContext.runAsUser` / `web.podSecurityContext.runAsUser`.

## Secret contract

The chart does **not** create secrets. The server reads them from an existing
Secret (`server.secrets.existingSecret`, default `tutti-frutti-secrets`):

| Key | Env var | Used for |
|---|---|---|
| `database-url` | `DATABASE_URL` | Postgres connection string (dedicated DB + user on the shared CloudPirates Postgres) |
| `session-secret` | `SESSION_SECRET` | Signing guest/session cookies |
| `google-client-id` | `GOOGLE_CLIENT_ID` | Google OIDC login |
| `google-client-secret` | `GOOGLE_CLIENT_SECRET` | Google OIDC login |
| `cloudflare-account-id` | `CLOUDFLARE_ACCOUNT_ID` | AI judge (optional, with the next key): Cloudflare account on the Free plan |
| `cloudflare-api-token` | `CLOUDFLARE_API_TOKEN` | AI judge: API token with only the Workers AI permission |

```bash
kubectl -n tutti-frutti create secret generic tutti-frutti-secrets \
  --from-literal=database-url='postgres://user:pass@host:5432/tuttifrutti?sslmode=disable' \
  --from-literal=session-secret="$(openssl rand -base64 32)" \
  --from-literal=google-client-id='<id>' \
  --from-literal=google-client-secret='<secret>'
```

The first four references are `optional: true` for now (`server.secrets.optional`) so
the Phase 0 placeholder deploy starts without the Secret. Set it to `false` once
the server needs the database / sessions / Google login so a missing Secret or
key fails the rollout loudly. Secret and key names can be changed under
`server.secrets`.

The two Cloudflare keys are always optional: without them the AI judge is off and the
host is never offered it (`server.judge.dailyNeurons` sets its daily Neuron budget, 9000 by
default; the Free plan allows 10000, and Cloudflare refuses calls after that).

Other server env vars: `PORT` (fixed `8080`), `LOG_LEVEL` (`server.logLevel`) and
`PUBLIC_URL` (derived from `ingress.host`; override with `server.publicUrl`).
Anything else goes into `server.extraEnv`.

## Values overview

| Value | Default | Description |
|---|---|---|
| `server.image.repository` / `.tag` | `ghcr.io/aresiusxp/tutti-frutti-server` / `""` | Empty tag = chart `appVersion` |
| `server.logLevel` | `info` | `LOG_LEVEL` |
| `server.publicUrl` | `""` | `PUBLIC_URL`; empty = `https://<ingress.host>` |
| `server.maxRooms` | `1000` | `MAX_ROOMS`: live rooms held in memory |
| `server.trustForwardedFor` | `false` | `TRUST_FORWARDED_FOR`: client IP for rate limits from the rightmost `X-Forwarded-For` entry |
| `server.allowedOrigins` | `[]` | `ALLOWED_ORIGINS` (comma-joined): extra origins besides the public URL (dev only) |
| `server.secrets.*` | see above | Existing Secret name, key names, `optional` |
| `server.extraEnv` | `[]` | Extra `EnvVar` entries |
| `server.resources` | 50m / 64Mi, 256Mi limit | |
| `web.replicaCount` | `1` | |
| `web.image.repository` / `.tag` | `ghcr.io/aresiusxp/tutti-frutti-web` / `""` | Empty tag = chart `appVersion` |
| `web.resources` | 10m / 16Mi, 128Mi limit | |
| `ingress.enabled` | `true` | |
| `ingress.className` | `traefik` | |
| `ingress.host` | `tuttifrutti.aresius.xyz` | |
| `ingress.annotations` / `ingress.labels` | cert-manager, external-dns, HSTS | external-dns needs **both** the `external-dns.aresius.xyz: pihole` annotation and label |
| `ingress.tls.enabled` / `.secretName` | `true` / `tuttifrutti.aresius.xyz-tls` | Empty secret name = `<host>-tls` |
| `serviceMonitor.enabled` | `false` | Optional Prometheus Operator `ServiceMonitor` for the server (`release: kube-prometheus-stack` label, port `http`, path `/metrics`); the CRD must exist |
| `dashboard.enabled` | `false` | ConfigMap with the Grafana dashboard (uid `tutti-frutti`) for the Grafana sidecar, see [Observability](#observability) |
| `dashboard.labels` | `{grafana_dashboard: "1"}` | ConfigMap labels the Grafana sidecar selects on (the homelab sidecar: `LABEL=grafana_dashboard`, `LABEL_VALUE=1`, `NAMESPACE=ALL`) |
| `dashboard.annotations` | `{}` | ConfigMap annotations, e.g. `{grafana_folder: Games}` when the sidecar sets `FOLDER_ANNOTATION` (the homelab one does not) |
| `prometheusRule.enabled` | `false` | `PrometheusRule` with the alerts (needs the Prometheus Operator CRD, `serviceMonitor.enabled`, kube-state-metrics and cAdvisor) |
| `prometheusRule.labels` | `{release: kube-prometheus-stack}` | Labels of the `PrometheusRule`; the homelab Prometheus `ruleSelector` only loads rules with this label |
| `prometheusRule.annotations` | `{}` | Annotations of the `PrometheusRule` object |
| `prometheusRule.interval` | `""` | Evaluation interval of the rule groups; empty = Prometheus default (30 s) |
| `prometheusRule.additionalRuleLabels` | `{}` | Extra labels on every alert (next to `severity`) |
| `prometheusRule.additionalRuleAnnotations` | `{}` | Extra annotations on every alert (next to `summary`, `description`, `runbook`) |
| `prometheusRule.runbookUrl` | DESIGN.md "Observability" anchor | `runbook_url` annotation of every alert; empty = omit |
| `nodeSelector`, `tolerations`, `affinity`, `imagePullSecrets`, `dnsConfig` | empty / ndots 1 | Applied to both pods |
| `containerSecurityContext` | hardened | Applied to both containers |

### Private images and charts

The packages (`tutti-frutti-server`, `tutti-frutti-web`, `charts/tutti-frutti`) can stay private.

* **Images:** create a `kubernetes.io/dockerconfigjson` Secret in the release namespace and reference it with
  `imagePullSecrets: [{name: ghcr-pull}]` (applied to both pods).
  ```bash
  kubectl create secret docker-registry ghcr-pull -n tutti-frutti \
    --docker-server=ghcr.io --docker-username=AresiusXP --docker-password="$GHCR_READ_TOKEN"
  ```
* **Chart:** ArgoCD needs a `repo-creds` template whose URL is **scheme-less** (`ghcr.io/aresiusxp`); see
  `AGENTS.md` in `AresiusXP/argocd-apps` ("Private GHCR packages").
* `GHCR_READ_TOKEN` is a classic PAT with only the `read:packages` scope.

Ingress routing: `/api` and `/ws` go to the server, `/` goes to web.
`/metrics`, `/healthz` and `/readyz` are intentionally **not** routed through the
ingress; Prometheus scrapes the server Service directly (enable
`serviceMonitor.enabled`) and kubelet probes hit the pod.

## Observability

Three optional resources, all **off by default**; the metric catalogue, the
dashboard layout and what to do when an alert fires are in
[`docs/DESIGN.md`](../../docs/DESIGN.md#observability-phase-5).

| Resource | Switch | Template |
|---|---|---|
| ServiceMonitor (scrapes the server `/metrics`) | `serviceMonitor.enabled` | `templates/servicemonitor.yaml` |
| Grafana dashboard ConfigMap | `dashboard.enabled` | `templates/grafana-dashboard.yaml`, content `dashboards/tutti-frutti.json` |
| PrometheusRule (14 alerts) | `prometheusRule.enabled` | `templates/prometheusrule.yaml` |

Homelab (kube-prometheus-stack, Alertmanager disabled): the ServiceMonitor is picked
up by every Prometheus (`serviceMonitorSelector: {}`); rules need the label
`release: kube-prometheus-stack` (default of `prometheusRule.labels`); the Grafana
sidecar imports ConfigMaps labelled `grafana_dashboard: "1"` from any namespace
into the "General" folder. Without Alertmanager the alerts show up in the
Prometheus UI (Alerts) and in Grafana (Alerting, Prometheus rules) but nothing notifies.

```bash
helm template t helm/tutti-frutti --set serviceMonitor.enabled=true \
  --set dashboard.enabled=true --set prometheusRule.enabled=true
```

Alert selectors use the release namespace and the server Service name (the
Prometheus `job` label) rendered by Helm, so a second release in another namespace
gets its own correctly scoped rules; the dashboard has `namespace` / `job`
variables instead.

**The dashboard JSON is generated.** Never edit `dashboards/tutti-frutti.json` by
hand: change `dashboards/generate.py` (Python 3, standard library only), run

```bash
python3 helm/tutti-frutti/dashboards/generate.py          # rewrite the JSON
python3 helm/tutti-frutti/dashboards/generate.py --check  # CI: fail when it is stale
```

and commit both. The ConfigMap is limited to 1 MiB (and ~256 KiB when ArgoCD stores a
copy in the `last-applied-configuration` annotation): the JSON is ~145 KB (one-space indent;
`dashboards/test_generate.py` keeps it well under that).

Checks without a cluster (needs `helm`, `python3` and [`promtool`](https://github.com/prometheus/prometheus/releases)):

```bash
PROMTOOL=/path/to/promtool helm/tutti-frutti/ci/check-observability.sh
```

It verifies that the JSON is up to date and valid, that the ConfigMap contains it
verbatim, that the PrometheusRule passes `promtool check rules` (every alert has
`severity`, `summary`, `description`, `runbook`, `for`) and runs the alert unit tests
in `tests/alerts_test.yaml` (`promtool test rules`: each alert fires when it should and
stays quiet during a normal deploy, a short database blip, ...; the snapshot alerts also
cover a restart, where the new pod reports `snapshot_last_success_timestamp_seconds` 0 until
its first save). The dashboard tests also grep the Go sources: a panel or alert that reads
a `tuttifrutti_*` series the server does not register, or a registered metric nobody
uses or documents, fails the check.
`dashboards/generate.py`, its tests, `tests/` and `ci/` are excluded from the packaged chart (`.helmignore`).

## How releases flow

1. Conventional commits land on `main` (squash-merge).
2. release-please keeps a release PR open that bumps `Chart.yaml`
   (`version` + `appVersion`) and `CHANGELOG.md`.
3. Merging that PR creates the `vX.Y.Z` tag; in the **same workflow run**
   (`.github/workflows/release.yml`) the server and web images are built and
   pushed as `ghcr.io/aresiusxp/tutti-frutti-{server,web}:X.Y.Z`, then this chart
   is packaged and pushed to `oci://ghcr.io/aresiusxp/charts`.
4. Renovate in the cluster bumps the chart version in `argocd-apps` and ArgoCD
   syncs.

To republish an existing tag (recovery), run the **Release** workflow manually
with the `tag` input (e.g. `v0.1.0`); already-published chart versions are skipped.
