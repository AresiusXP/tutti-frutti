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

```bash
kubectl -n tutti-frutti create secret generic tutti-frutti-secrets \
  --from-literal=database-url='postgres://user:pass@host:5432/tuttifrutti?sslmode=disable' \
  --from-literal=session-secret="$(openssl rand -base64 32)" \
  --from-literal=google-client-id='<id>' \
  --from-literal=google-client-secret='<secret>'
```

All four references are `optional: true` for now (`server.secrets.optional`) so
the Phase 0 placeholder deploy starts without the Secret. Set it to `false` once
the server needs the database / sessions / Google login so a missing Secret or
key fails the rollout loudly. Secret and key names can be changed under
`server.secrets`.

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
