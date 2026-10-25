# Tutti Frutti

Real-time multiplayer **Tutti Frutti** for phones — play in the
browser, install it as an app, share a 4-letter code with friends.

* Play: <https://tuttifrutti.aresius.xyz>
* Design, rules, protocol and UX: [`docs/DESIGN.md`](docs/DESIGN.md)
* Contributor / agent rules: [`AGENTS.md`](AGENTS.md)

## How the game works

One letter per round, a handful of categories (name, animal, country, …). Everyone
writes a word that starts with the letter; the first to finish calls **¡Basta!**
("Stop!" in English) and everybody stops. Then every category is reviewed together:
a unique answer is worth **10** points, an answer someone else also wrote **5**, an
empty one **0**. Players can vote to reject invalid answers. Most points wins.

## Repository layout

| Path | What |
|---|---|
| `server/` | Go game server (REST + WebSocket, authoritative engine) |
| `web/` | SvelteKit static PWA (Spanish/English) |
| `helm/tutti-frutti/` | Helm chart (web + server + ingress) |
| `e2e/` | Playwright end-to-end tests (later phase) |
| `docs/` | Design documentation |
| `.github/workflows/` | CI, and the release pipeline |

## Local development

```bash
# everything (postgres + server + web + Caddy edge on http://localhost:8000)
docker compose up --build

# or individually
cd server && go run ./cmd/server          # :8080
cd web && npm ci && npm run dev           # :5173, proxies /api and /ws to :8080
```

Checks (same as CI):

```bash
cd server && go vet ./... && go test -race ./...
cd web    && npm run check && npm run lint && npm run test && npm run build
helm lint helm/tutti-frutti
```

## Releases & deployment

Conventional commits on `main` → **release-please** opens a release PR → merging it
tags `vX.Y.Z`, builds `ghcr.io/aresiusxp/tutti-frutti-{server,web}:X.Y.Z` and pushes
the chart to `oci://ghcr.io/aresiusxp/charts`. In-cluster Renovate bumps the chart in
[`AresiusXP/argocd-apps`](https://github.com/AresiusXP/argocd-apps) and ArgoCD rolls it out.

**First release order:** the `tutti-frutti` entry in `argocd-apps` points at chart `0.1.0`, which only
exists after the first release. Merge the release PR (publishes images + chart) **before** merging the
`argocd-apps` change; otherwise the app just shows a chart-fetch error until `0.1.0` is published.

### One-time cluster prerequisites

1. A database + user for the game on the shared Postgres (needed from Phase 2).
2. Secret `tutti-frutti-secrets` in namespace `tutti-frutti` with keys
   `database-url`, `session-secret`, `google-client-id`, `google-client-secret`
   (plus `cloudflare-account-id` and `cloudflare-api-token` to turn on the optional AI judge).
3. A public DNS record for `tuttifrutti.aresius.xyz`: add `tuttifrutti` to `dnsRecords` in `argocd-apps/values/porkbun-cronjob.yaml` (the hourly Porkbun cronjob creates it).
4. GHCR packages `tutti-frutti-server`, `tutti-frutti-web` and `charts/tutti-frutti` may stay
   **private**: ArgoCD needs a scheme-less `repo-creds` template (`ghcr.io/aresiusxp`), the
   `tutti-frutti` namespace needs a `ghcr-pull` image pull secret, and Renovate needs a `ghcr.io`
   host rule. Details: `AGENTS.md` in `AresiusXP/argocd-apps`, section "Private GHCR packages".
5. Google OAuth client (Phase 4) with redirect URI
   `https://tuttifrutti.aresius.xyz/api/auth/google/callback`.
