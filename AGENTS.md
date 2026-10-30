# AGENTS.md — tutti-frutti

Real-time multiplayer Tutti Frutti word game (mobile-first web PWA).
**Read `docs/DESIGN.md` first** — it is the source of truth for rules, protocol,
UX and SDLC. If code and design diverge, update the design in the same change.

## Product rules you must not break

* The game's name is **"Tutti Frutti"** in every language. It is a fixed
  constant, never a translation key. "Basta" is **not** a name or alias of the
  game: it is only the call a player makes to end a round.
* English and Dutch screens say just **"Stop!"** for the Basta call (no Spanish
  word or character); Spanish says **¡BASTA!**.
* Server is authoritative. Clients only send intents.
* The game engine (`server/internal/game`) is **pure**: no I/O, no goroutines,
  no `time.Now()` (time is passed in), no global randomness (injected source).

## Deployment target

Runs in a **microk8s homelab** (GitOps via `AresiusXP/argocd-apps`, ArgoCD
app-of-apps, Traefik ingress, cert-manager `letsencrypt`, external-dns Pi-hole,
Longhorn storage, shared CloudPirates Postgres — **never Bitnami**).

* Hostname: `tuttifrutti.aresius.xyz` (TLS secret `tuttifrutti.aresius.xyz-tls`).
* Server is a **single replica** with `strategy: Recreate` (state is in memory;
  see design). Do not scale it out without redesigning state.
* Images: `ghcr.io/aresiusxp/tutti-frutti-server`, `ghcr.io/aresiusxp/tutti-frutti-web`.
  Chart: `oci://ghcr.io/aresiusxp/charts/tutti-frutti`.
* Prefer explicit image tags over `latest`; keep resource requests small
  (server ~50m/64Mi, web ~10m/16Mi).
* Secrets come from the existing Secret `tutti-frutti-secrets`
  (`database-url`, `session-secret`, `google-client-id`, `google-client-secret`,
  and the optional `cloudflare-account-id`, `cloudflare-api-token` of the AI judge).
* The Android app (`android/`, package `xyz.aresius.tuttifrutti`) is a **Trusted Web Activity** of
  this same website: no WebView, no native game code, no permissions. Web changes reach it through
  the website; only shell changes need a Play release (`docs/ANDROID.md`, `docs/PLAY-RELEASE.md`).
  The Cafecito/Ko-fi support links stay **hidden inside the Android app** (Google Play payments
  policy, gate G2); the website keeps them.
* Public play depends on the content-safety layer (terms, reports, blocks, operator `/moderate`
  CLI; `docs/MODERATION.md`). Never weaken it without updating the Play declarations.
* The AI judge (review, `server/internal/judge`) runs on **Cloudflare Workers AI, Free
  plan** (10,000 Neurons/day, refused after that, never billed). **Never add a payment
  method to that Cloudflare account or upgrade it to Workers Paid**, and never use a
  second account to get more quota (the terms forbid it). Gemini's free tier is not an
  option: its terms bar EEA/minor users (see `docs/DESIGN.md`, "AI judge").

## Commands

```bash
# server/
go build ./... && go vet ./... && go test -race ./...
# TS protocol types (web/src/lib/protocol/*.ts): regenerate after changing
# internal/protocol, internal/game or internal/catalog; never hand-edit, CI fails on drift
go tool tygo generate
# web/
npm ci && npm run check && npm run lint && npm run test && npm run build
# helm
helm lint helm/tutti-frutti && helm template t helm/tutti-frutti
# android/ (JDK 17 + Android SDK; see docs/ANDROID.md)
./gradlew testDebugUnitTest lintRelease assembleDebug bundleRelease && node --test scripts/*.test.mjs
# everything locally
docker compose up --build
```

## Conventions

* Conventional commits (`feat:`, `fix:`, `chore:`, `docs:` …) — release-please
  derives versions and changelog from them. Squash-merge PRs.
* One version for the whole repo (`vX.Y.Z`), stored in `helm/tutti-frutti/Chart.yaml`.
* Go: stdlib first, `log/slog`, table-driven tests, `-race` clean.
* Web: Svelte 5 (runes), TypeScript strict, Tailwind, Paraglide messages in
  `web/messages/{es,en,nl}.json` plus one folder per area
  (`web/messages/<area>/{es,en,nl}.json`); every key exists in all three languages.
  Touch targets ≥ 48 px.
* YAML: 2 spaces, no tabs, one trailing newline.
* Never commit secrets. Never delete the `values/` entries of other apps.
