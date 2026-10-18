# Tutti Frutti — Design

> Source of truth for product rules, architecture, protocol and SDLC.
> The game is called **Tutti Frutti** in every language. The name is a fixed
> constant and is **never** placed in the translation files.

## 1. Product summary

Real-time multiplayer web game (mobile-first, installable PWA) based on the
Argentine game *Tutti Frutti* / *Basta*. Hosted on the homelab at
**https://tuttifrutti.aresius.xyz**.

| Area | Decision |
|---|---|
| Client | SvelteKit (adapter-static) + Tailwind + Paraglide i18n, installable PWA. UI languages: Spanish (Argentina) and English; a first visit follows the browser language (Spanish for any other), the ES/EN switch is remembered |
| Server | Go, WebSockets, **single replica**, authoritative game state in memory, snapshots to Postgres on every phase change (and every ~5 s while playing) |
| Identity | Nickname + avatar (emoji + colour) for guests. Optional Google login (OIDC) adds profile + lifetime stats |
| Lobbies | Private, 4-char code, invite link, QR. Max 10 players |
| Letter | Hidden alphabet cycles from A; the round's *chooser* taps STOP. Used letters are skipped |
| Basta | The caller triggers a 3 s grace countdown for everyone else, then inputs lock |
| Review | Players vote to reject invalid answers (majority of other players); answers can be merged as "the same" by majority |
| Extras v1 | Invite link + QR, optional round time limit, custom per-game categories, installable PWA |

### Naming rules

* "Tutti Frutti" is a fixed constant (manifest `name`/`short_name`, `<title>`,
  logo, share text). Never translate it.
* Spanish screens: the call button is **¡BASTA!**, the overlay says
  "¡Basta para mí, basta para todos!".
* English screens: the call button and the overlay just say **"Stop!"**.
  (No Spanish subtitle.)
* The chooser's letter-stopping button is "¡STOP!" (es) / "Stop!" (en).

## 2. Architecture

```
 Phone browser / PWA ──HTTPS/WSS──▶ Traefik (tuttifrutti.aresius.xyz, Let's Encrypt)
                                     ├─ /        → web    (nginx-unprivileged, static SvelteKit build)
                                     ├─ /api/*   → server (Go REST)
                                     └─ /ws      → server (Go WebSocket)
                                                   └─ Postgres (users, stats, room snapshots)
```

### Server (Go)

* **Server is authoritative.** Clients send intents only. The server owns the
  clock, picks the letter, validates answers, computes scores.
* **One goroutine per room** (actor) owns the room state; commands are handled
  one at a time (no locks). Timers use an injectable clock.
* After every change each client gets a **full filtered snapshot** (`state`).
  Other players' answers are hidden while `PLAYING`/`GRACE`; only fill-progress
  is visible.
* **Restarts**: snapshot to Postgres; on startup reload and let clients
  reconnect with backoff. Deployment is `strategy: Recreate`, replicas: 1.
* **Identity**: signed `HttpOnly` guest cookie (random guest ID) lets a player
  reclaim their seat. Google OIDC (code flow + PKCE) is handled by the server and
  sets a session cookie. WebSocket is same-origin, authenticated by cookie, and the
  `Origin` header is checked.
* **Client version check**: client sends protocol version on connect. On
  mismatch the app shows "Nueva versión disponible → Recargar" / "New version
  available → Reload".

### Postgres

Reuse the existing CloudPirates Postgres in the cluster with a dedicated
database + user. Tables: `users`, `user_stats`, `room_snapshots`. Migrations
(goose) are embedded in the server binary and run at startup (safe with one
replica). The category catalog is an embedded JSON file, not a table.

## 3. Game rules (as implemented)

**Phases:** `LOBBY → LETTER → PLAYING → GRACE → REVIEW → ROUND_SUMMARY → (LETTER | FINAL)`

### Lobby settings (host only; others read-only)

| Setting | Options | Default |
|---|---|---|
| Rounds | 1 … number of available letters, or unlimited | 5 |
| Categories | from catalog + custom; **3–10 total** | the 6 "Clásicas" |
| Lobby language | `es` / `en` — sets alphabet, hard-letter list, category labels | `es` |
| Exclude hard letters | on/off | on |
| Time limit | off, 60, 90, 120, 180 s | off |

* Alphabets: `es` = A–Z with **Ñ** after N (27 letters); `en` = A–Z (26).
* Hard letters: `es` = **K Ñ Q W X Y Z**; `en` = **Q U V X Y Z**. Kept in a data
  file / table that is easy to edit.
* A player's *screen* language is independent from the *lobby* language.
  Category labels always render in the **lobby** language.
* Settings can only change in `LOBBY` (and again after a rematch).

### Letter phase

* Chooser rotates: the host in round 1, then players in join order (skip
  players who are gone/kicked).
* Server cycles the remaining (unused, non-excluded) letters starting from the
  first available one at **120 ms/letter**; nobody sees the cycle ("silent
  alphabet"). The chooser taps STOP; the server derives the letter from the
  elapsed time: `letters[floor(elapsed/120ms) mod len(letters)]`.
* If the chooser does not tap within **10 s**, the server auto-stops it.

### Playing phase

* Answers are saved automatically (debounced client-side, plus on blur), so
  the server already has them when Basta is called.
* **Basta** is only enabled when every field is filled (client-side hint); the
  server validates the caller has every category non-empty and starting with the
  letter, otherwise rejects the call.
* Server-side letter check: an answer that does not start with the round's
  letter (after normalization) scores 0 and is flagged `wrong_letter`.
* If a time limit is set and expires, the room moves to `GRACE`/lock with no
  caller.

### Basta / grace

* Everyone sees the full-screen overlay + vibration where available. A **3 s**
  grace countdown starts; then answers lock. Updates arriving up to **500 ms**
  after the lock are still accepted (network lag allowance).

### Answer normalization & matching

Lowercase, trim, collapse internal whitespace, strip diacritics
(`México` == `mexico`). In **Spanish** lobbies `ñ` stays a distinct letter
(it is not folded into `n`), and the starting-letter check treats `ñ` as its own
letter. Two normalized answers within a category are "the same".

### Review

* One category at a time; the host navigates (◀ ▶), everyone sees the same card.
* Each answer shows its points badge (10 unique / 5 shared / 0 empty or
  invalid), live.
* **Reject vote:** a player votes against someone else's answer. It is rejected
  when **more than half of the other players (everyone except the author, among
  connected-or-seated players who took part in the round)** vote to reject.
  A rejected answer scores 0 and no longer counts as a duplicate for others.
* **"Son iguales" / "Same"**: propose that two (or more) answers are the same;
  merged when a majority of players who took part vote yes. Merged answers count as
  duplicates of each other.
* Host taps *Finish review* → scores final.

### Scoring (pure function)

Per category, per player:

* empty / invalid (wrong letter / rejected) → **0**
* valid and unique among valid answers (after merges) → **10**
* valid and shared with ≥ 1 other valid answer → **5**

Round total = sum over categories; added to the player's running total.

### Round summary / final

* Summary: round points + running totals + leaderboard; host taps *Next round*
  or *End game*. Game also ends when the round count is reached or no letters
  are left.
* Final: podium; **Rematch** (same lobby & settings incl. custom categories,
  scores reset); stats updated for logged-in players.

### Lobby management

* Host leaves → longest-connected player becomes host.
* Host can kick players.
* Disconnected players keep their seat for the whole game.
* Joining mid-game: starts playing next round with 0 points.

## 4. Category catalog

* `server/internal/catalog/categories.json`, embedded; served at
  `GET /api/categories?lang=`.
* Each entry: `id` (stable short key), `group`, `es`, `en`, `default` (bool).
  Saved room state stores only ids (+ custom labels).
* **Exactly 50 entries**, **exactly 6 `default`** ("Clásicas": Nombre, Animal,
  País, Color, Comida, Marca).
* **Custom categories**: per-game only; stored in room state, copied on rematch,
  never added to the catalog. ≤ 24 chars, non-empty, no duplicates after
  normalization (also vs. built-in labels in both languages). Total 3–10.

| Group | Categories (ES / EN) |
|---|---|
| personas (6) | Nombre / Name ★ · Apellido / Last name · Famoso/a / Celebrity · Personaje histórico / Historical figure · Futbolista / Football player · Cantante o banda / Singer or band |
| lugares (8) | País / Country ★ · Ciudad / City · Capital / Capital city · Provincia o estado / Province or state · Río, lago o mar / River, lake or sea · Lugar turístico / Tourist attraction · Nacionalidad / Nationality · Club de fútbol / Football club |
| naturaleza (4) | Animal / Animal ★ · Ave / Bird · Animal marino / Sea creature · Flor o planta / Flower or plant |
| comida (5) | Comida / Food ★ · Fruta o verdura / Fruit or vegetable · Postre / Dessert · Bebida / Drink · Algo que hay en la heladera / Something in the fridge |
| cosas (7) | Cosa / Thing · Color / Color ★ · Ropa o accesorio / Clothing or accessory · Mueble / Furniture · Útil escolar / School supply · Juguete / Toy · Medio de transporte / Vehicle |
| marcas (2) | Marca / Brand ★ · Marca de auto / Car brand |
| cultura (6) | Película / Movie · Serie / TV show · Dibujo animado / Cartoon · Personaje de ficción / Fictional character · Superhéroe o villano / Superhero or villain · Videojuego / Video game |
| trabajo (4) | Profesión u oficio / Job · Deporte / Sport · Instrumento musical / Musical instrument · Parte del cuerpo / Body part |
| divertidas (8) | Adjetivo / Adjective · Verbo / Verb · Idioma / Language · Emoción / Emotion · Nombre de mascota / Pet name · Excusa para llegar tarde / Excuse for being late · Algo que llevás a la playa / Something you take to the beach · Algo que da miedo / Something scary |

★ = default.

## 5. Interfaces

### REST (`/api`)

`POST /rooms` · `GET /rooms/{code}` · `GET /session` · `GET /me` ·
`GET /me/stats` · `GET /auth/google/login?returnTo=` · `GET /auth/google/callback`
· `POST /auth/logout` · `GET /categories?lang=` · plus `/healthz`, `/readyz`,
`/metrics` at the root.

### WebSocket (`/ws`)

Envelope `{ "t": string, "d": object, "v": protocolVersion }`.

* client → server: `join`, `leave`, `update_settings`, `kick`, `start_game`,
  `stop_letter`, `set_answer`, `basta`, `vote_reject`, `propose_merge`,
  `vote_merge`, `review_nav`, `finish_review`, `next_round`, `end_game`,
  `rematch`, `ping`
* server → client: `state` (filtered full snapshot), `event`, `error`.
  Engine event types: `game_started`, `round_started` (carries the chooser),
  `letter_revealed`, `basta`, `time_up`, `round_locked`, `round_scored`, `kicked`,
  `host_changed`, `game_over`, `rematch`; the room layer adds `server_restarting`.
* `vote_reject` takes `{category, targetPlayer, withdraw?}` (a vote can be withdrawn).
  Connection state is **not** a wire command: the room layer calls the trusted
  `Game.SetConnected(now, playerID, connected)` on connect/disconnect, and a wire
  `set_connected` is rejected as an unknown command (a client must not be able to
  skew vote thresholds).
* App-level ping every 20 s keeps connections alive through Traefik.
* TypeScript protocol types are generated from Go structs with `tygo`; CI
  fails if the generated file is stale.

**Room codes:** 4 chars from `BCDFGHJKLMNPQRSTVWXZ23456789` (no vowels → no
words; no look-alikes). Invite link: `https://tuttifrutti.aresius.xyz/KT7P`.

Limits: answer ≤ 40 chars, nickname ≤ 16, custom category ≤ 24.

### Engine decisions (Phase 1)

* **Deadlines are exclusive:** a transition happens when `now >= deadline`; `Apply`
  runs `Tick(now)` first, so a command arriving after a deadline sees the new phase.
* **Time-limit expiry** enters `grace` with no caller (only the 500 ms late-update
  window), emits `time_up`, then `round_locked`.
* **No letter-phase leak:** `View` never exposes when the alphabet started or its
  deadline, so the chooser cannot work out the letter.
* **Votes are evaluated live** against the players connected at that moment. Reject
  needs `yes*2 > connected participants except the author`; merge needs
  `yes*2 > connected participants` (the proposer counts as yes).
* **Leaving:** in the lobby a leave/kick removes the seat; in a started game the seat is
  flagged `Left` (scores stay, no more votes, skipped as chooser). Kicked players are
  banned for the life of the game (also after a rematch); a player who left voluntarily
  may reclaim their seat if one is free.
* **Joining:** brand-new players are rejected in `final` (`ErrWrongPhase`); they can join
  again once the host starts the rematch (back in `lobby`). Reclaiming an existing seat is
  always allowed.
* **Late ticks catch up:** the chooser auto-stop is dated at the chooser deadline (not at
  the time the tick runs), then every elapsed deadline is processed in order.
* **Normalization** is Unicode-based (NFD, drop combining marks; `ñ` kept in `es`).
* **Winners** exclude players who left; `next_round`/`end_game` only from
  `round_summary`; `rematch` only from `final`.

## 6. UX (one main action per screen, thumb-reachable bottom buttons)

1. **Home** — logo, nickname, avatar (emoji + colour), **Create game**, **Join**
   (4-box code input), links: Google login, ES/EN.
2. **Lobby** — huge code, **Share** (Web Share API), **QR**, player list with 👑,
   settings card (host editable), category picker (search, groups, "Clásicas"
   and "Sorprendeme" presets, reorderable selection, custom category input,
   recent customs from `localStorage`), **Start** (≥ 2 players).
3. **Letter** — chooser: spinning "?" + huge **STOP** button; others: "X is
   choosing the letter…"; animated reveal.
4. **Playing** — giant letter, optional timer, other players' progress dots,
   one field per category (Enter → next field, autocorrect off, capitalized),
   sticky **BASTA!/Stop!** button (disabled until all valid), Screen Wake Lock.
5. **Basta overlay** — caller name + 3-2-1.
6. **Review** — category carousel ("País · 2/6"), one card per answer (player,
   answer, points badge, 👎 count, "same"), host ◀ ▶ + **Finish review**.
7. **Round summary** — round points, totals, **Next round** (host).
8. **Final** — podium, stats, **Rematch**, **Home**.

Accessibility: touch targets ≥ 48 px, high contrast, `aria-live` for Basta and
letter reveal.

The room route also has non-phase screens: an invite **gate** (`/KT7P` in a fresh tab: room check,
nickname, avatar, Join), a "Conectando…" screen, a "Reconectando…" banner over the last screen and
a **closed** screen per reason (room gone, kicked, replaced by another tab, new version, join
refused). See "Web client architecture" in 9a.

## 7. Repository layout

```
server/                Go module (github.com/AresiusXP/tutti-frutti/server)
  cmd/server/          main
  internal/game/       pure engine + scoring (no I/O)
  internal/catalog/    embedded categories.json
  internal/room/       actor/hub                         (Phase 2)
  internal/ws/         WebSocket                         (Phase 2)
  internal/httpapi/    REST, health, metrics
  internal/auth/       guest cookie + Google OIDC        (Phase 4)
  internal/store/      Postgres (pgx)                    (Phase 2/4)
  migrations/
web/                   SvelteKit static PWA
helm/tutti-frutti/     one chart: web + server + ingress
e2e/                   Playwright (Phase 5)
docs/                  this file
.github/workflows/     CI + release
```

## 8. CI/CD & process

* **Pull requests** (path-filtered): `ci-server` (vet, staticcheck, race tests,
  build, tygo drift), `ci-web` (svelte-check, lint, vitest, build), `ci-helm`
  (lint, template, kubeconform), `e2e` (manual / when both change).
* **One version for everything** (server + web share the WS protocol).
  `release-please` opens a release PR from conventional commits; it bumps
  `helm/tutti-frutti/Chart.yaml` (`version` + `appVersion`). Merging the release
  PR creates tag `vX.Y.Z` **and, in the same workflow run**, builds/pushes
  `ghcr.io/aresiusxp/tutti-frutti-server:X.Y.Z` and
  `ghcr.io/aresiusxp/tutti-frutti-web:X.Y.Z`, then packages & pushes the Helm
  chart to `oci://ghcr.io/aresiusxp/charts` (tags created by `GITHUB_TOKEN`
  do not trigger other workflows, so publishing lives in the same workflow).
  Chart image tags default to `.Chart.AppVersion`.
* **Delivery**: in-cluster Renovate bumps the chart version in
  `argocd-apps/bootstrap/values.yaml` (branch automerge for this chart); ArgoCD syncs.
* GitHub flow, squash merges, conventional commits.

## 9. Phases

0. Repo + pipeline skeleton (placeholder deploys at the hostname).
1. Game engine + catalog (pure, heavily tested).
2. Rooms, WebSocket protocol, persistence, tygo types (contracts in section 9a). **Must include a WebSocket drain on
   shutdown:** `http.Server.Shutdown` does not wait for hijacked connections, so track open
   sockets, send `server_restarting`, close them (code 1012) and wait for the drain inside the
   existing budget (2 s readiness grace + 20 s drain < 30 s `terminationGracePeriodSeconds`),
   after snapshotting all rooms.
3. Web UI (all screens, i18n, category picker).
4. Accounts (Google OIDC, stats).
5. PWA polish, e2e, metrics/dashboard, security headers.
6. Beta with friends, tune timings, v1.0.0.

## 9a. Phase 2 — rooms, transport and persistence (contracts)

This section is the contract between the packages built in Phase 2. The wire
types live in `server/internal/protocol` (generated to TypeScript with `tygo`);
the persistence contract is `server/internal/store`.

### Packages

| Package | Responsibility |
|---|---|
| `internal/protocol` | Wire types, error codes, close codes, limits (no logic) |
| `internal/store` | `Store` interface + `Memory` + `Postgres` (pgx, goose migrations embedded) |
| `internal/room` | `Hub` (registry, room codes, reaper, restore, shutdown) and one actor goroutine per room that owns a `*game.Game` |
| `internal/auth` | Signed guest cookie (`tf_guest`) → player ID |
| `internal/ratelimit` | Per-IP token buckets + trusted client-IP extraction |
| `internal/ws` | WebSocket endpoint: upgrade, Origin check, read/write pumps, ping, per-connection limits |
| `internal/httpapi` | REST routes (`/api/session`, `/api/categories`, `/api/rooms`, `/api/rooms/{code}`) |
| `cmd/server` | Wiring and the shutdown sequence |

### Identity

* The player ID **is** the guest ID from the cookie. `GET /api/session` (and
  `POST /api/rooms`) mint it if missing: `tf_guest = <id>.<mac>`, `id` = 16
  random bytes base64url, `mac` = HMAC-SHA256(`SESSION_SECRET`, id) base64url;
  `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when `PUBLIC_URL` is https,
  `Max-Age` 1 year. A forged/invalid cookie is treated as absent (a new ID is
  minted on REST; the WebSocket answers `unauthorized`).
* `SESSION_SECRET` empty → a random ephemeral secret is generated at startup
  and a warning is logged (sessions will not survive a restart). Production sets it.
* All unsafe REST methods and the WebSocket upgrade check `Origin` against
  `PUBLIC_URL`'s host plus `ALLOWED_ORIGINS` (comma-separated, for dev:
  `http://localhost:5173,http://localhost:8000`); a request without `Origin` is
  allowed only on safe methods. JSON bodies are limited to 4 KiB.

### Connection flow

1. Client calls `POST /api/rooms {name, avatar, lang?}` (creates the room; the
   caller is the host seat, still disconnected) or learns a code from a friend.
   `GET /api/rooms/{code}` → `{code, phase, players, maxPlayers, lang, joinable}`
   or 404 `room_not_found`. While the server drains (deploy/reboot) an unknown
   room answers **503 `server_busy` + `Retry-After: 2`** instead of 404: the room
   is restored from its snapshot by the next pod, so clients must retry, never
   give up. The same holds for the WebSocket (`server_busy` + close 1012).
2. Client opens `wss://host/ws` (cookie authenticates). The **first frame must be
   `join`** (`protocol.JoinRequest`, `v: 1`). The server looks the room up,
   applies `game.Join` and then the trusted `Game.SetConnected(now, id, true)`.
3. After every accepted command the room broadcasts `state` (a per-viewer
   `game.View`) to all connected clients, preceded by `event` frames for the
   engine events produced by that command or tick.
4. A command rejected by the engine returns `error {code, ref}` to the sender
   only; the connection stays open.
5. Reconnect = open a new socket and `join` again (same cookie → same seat;
   score and answers are kept; the engine's `join` never changes connection
   state, the room layer calls `SetConnected`). A second live connection of the
   same player replaces the first (`event replaced`, close `4001`).
6. `leave` → `game.Leave`, close `4005`. Kick → `event kicked`, close `4002`.
   Socket drop → `SetConnected(false)` (the seat is kept for the whole game).

### Room lifecycle (internal/room)

* **Actor:** one goroutine per room owns the `*game.Game`; everything (client
  frames, timer fires, connect/disconnect, snapshot requests) goes through its
  inbox, so no locks are needed. Handlers never block on a slow client: each
  client has a bounded send queue (`protocol.SendQueueSize`); on overflow the
  client is closed (1008) and will reconnect for a fresh snapshot.
* **Timers:** after every command/tick the room arms a single timer at
  `Game.NextDeadline()` (injectable `Clock`); on fire it calls `Tick(now)` and
  broadcasts. Late ticks catch up (engine guarantees).
* **Single timer:** the one per-room timer covers the engine deadline, periodic
  and heartbeat snapshots, the join deadlines and the idle/unclaimed deadlines (the
  idle reaper is therefore not a central goroutine; only the snapshot janitor is).
* **Room codes:** 4 characters from `BCDFGHJKLMNPQRSTVWXZ23456789`, crypto/rand,
  retry on collision; `MaxRooms` (default 1000) → `server_busy`.
* **Closing:** a room with nobody connected for `IdleTTL` (30 min) is closed; a
  room created over REST that nobody connected to within `UnclaimedTTL` (2 min)
  is closed. Closing deletes its snapshot and closes sockets with `4004`.
* **Persistence:** the room marshals its state (`room.Persisted{version, code,
  createdAt, lastActive, game}`) inside the actor and hands the bytes to an
  asynchronous, per-room latest-wins saver (never blocks the actor, retries with
  backoff, logs failures). It saves on every phase change, on settings/lobby
  changes, and every `SnapshotEvery` (5 s) while a round is `playing`/`grace`.
  Every live room also re-saves at least every `SnapshotMaxAge/4` (heartbeat),
  so the janitor (`Store.DeleteOlderThan(6 h)`, hourly) can never delete a live
  room's snapshot. A failed snapshot delete is retried with backoff and keeps the
  room code reserved until it succeeds (otherwise a stale row could resurrect an
  old room under a reused code).
* **Restore (startup):** `Hub.Start` loads all snapshots, rebuilds the rooms with
  **every player disconnected** (`SetConnected(false)`; nobody has a socket yet),
  sets `lastActive = now` (so the idle clock restarts) and re-arms timers.
  Corrupt/unknown-version snapshots are logged and deleted.
* **Shutdown:** `Hub.Shutdown(ctx)`: stop accepting joins → broadcast `event
  server_restarting` → flush a final snapshot of every room (synchronously) →
  close every socket with `1012` → wait for the pumps to finish or ctx to expire.
  `cmd/server` order: `/readyz` 503 → 2 s grace → `Hub.Shutdown` (≤ 15 s) →
  `ws.Handler.Shutdown` (≤ 3 s: sends `server_restarting` + 1012 to the sockets the
  hub does not track and waits for every connection goroutine, since
  `http.Server.Shutdown` ignores hijacked connections) → `http.Server.Shutdown`
  (≤ 5 s) → store close (total 25 s < `terminationGracePeriodSeconds` 30).

### Limits and abuse protection

* Frame ≤ `protocol.MaxMessageBytes` (4 KiB) → `message_too_large` + close 1009.
* Per connection: `ClientRatePerSecond`/`ClientBurst` token bucket → on
  exhaustion `rate_limited` and close 1008. 10 malformed frames → close 1008.
* App-level `ping` every 20 s (`pong` carries `serverTime`); no complete frame
  for 60 s → close (the read is cancelled, so a peer stalling mid-frame is
  dropped too; once a frame header arrives the rest must follow within 10 s).
* **Join deadline:** a socket must send `join` within 10 s (`protocol.JoinTimeout`;
  pings do not count) or it is closed `1008 "join timeout"`; the room then owns a
  second deadline (`room.Config.JoinTimeout`, 10 s) for the seat: a rejected join
  does not extend it. A room holds at most `2 × MaxPlayers` unjoined sessions
  (beyond that → `error server_busy` + 1008) and is only "claimed" (unclaimed TTL
  stops) after a successful join; unjoined sessions never keep a room alive.
* **Socket caps:** at most `MaxConns` (2000) sockets in total → `503 server_busy`
  and `MaxConnsPerGuest` (4) per guest → `429 too_many_connections`, both before
  the upgrade and without spending the upgrade bucket. Requests rejected by the
  cookie/Origin/upgrade checks never spend the per-IP bucket either (otherwise one
  bad actor could exhaust the shared bucket). Gauge `tuttifrutti_ws_connections_open`.
* `SESSION_SECRET`, when set, must be at least 32 bytes (startup error otherwise).
* Per IP (client IP from the **rightmost** `X-Forwarded-For` entry when
  `TRUST_FORWARDED_FOR=true`, else `RemoteAddr`; IPv6 keyed by /64): room creation
  30/min (burst 10), `GET /api/rooms/{code}` and WebSocket upgrades 300/min
  (burst 60) → `429 rate_limited` with `Retry-After`. Buckets are evicted when idle.
  **Homelab caveat:** the Traefik Service uses `externalTrafficPolicy=Cluster`, so
  every client appears as the same SNAT'd pod IP and these buckets act as one
  *global* bucket; the numbers are sized for that (a group of friends and a
  reconnect storm after a deploy never hit them). Switching the Service to
  `Local` would make them truly per-client.
* Metrics (Prometheus): `tuttifrutti_rooms` (gauge), `tuttifrutti_connections`
  (gauge), `tuttifrutti_commands_total{type,result}`, `tuttifrutti_rounds_total`,
  `tuttifrutti_basta_total`, `tuttifrutti_snapshot_errors_total`,
  `tuttifrutti_ws_closes_total{code}`.

### Persistence schema (goose, embedded, run at startup)

```sql
CREATE TABLE room_snapshots (
  code        text PRIMARY KEY,
  state       jsonb NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX room_snapshots_updated_at_idx ON room_snapshots (updated_at);
```

`users` / `user_stats` arrive with Phase 4. `DATABASE_URL` empty → in-memory
store with a warning (games do not survive restarts); `/readyz` also pings the
store when it is a database.

### Web client architecture (Phase 3)

File layout (`web/src`, details in `web/README.md`):

```
lib/game/session.ts        RoomSession: the contract between screens and the network
lib/game/client.svelte.ts  WsRoomSession (+ backoff.ts, transport.ts, roomMark.ts)
lib/game/fake.svelte.ts    FakeSession for tests and the /dev playground
lib/api.ts                 REST client, typed ApiError
lib/protocol/*             generated wire types (tygo), never hand-edited
lib/screens/*              one screen per phase, picked by RoomScreen.svelte
lib/components/            JoinGate, ConnectingScreen, ConnectionBanner, ClosedScreen, ...
routes/+page.svelte        Home (create / join)
routes/[code=roomcode]/    the room: gate -> connecting -> phase screens -> closed
```

Screens only see a `RoomSession` (`status`, `closedReason`, `view`, derived `players`/`me`/`isHost`/
`isChooser`/`isParticipant`, `events`, `lastError`, `serverNow()` and one method per command). Commands
never throw; a rejection arrives as `lastError`. Deadlines are always compared with `serverNow()`
(`Date.now() + offset`, offset from `State.serverTime` using the lowest round-trip sample).

**Session states:** `connecting` (before the first `state`) → `connected` → `reconnecting` (socket lost,
last `view` kept) → `connected` …, or `closed` with a reason: `room_gone`, `kicked`, `left`,
`replaced`, `unsupported_version`, `join_rejected` (+ `joinError`). A closed session never reconnects.

**Reconnect policy** (every attempt: `GET /api/session` once, preflight `GET /api/rooms/{code}`, open
`/ws`, `join` first):

* **Never give up on 502 / 503 / 504 / network errors / 429**: the server is a single replica, every
  deploy leaves a gap of a few seconds (Traefik answers `503 no available server`; Go answers `503
  server_busy` + `Retry-After` for unknown rooms while draining). Exponential backoff with full
  jitter, 500 ms → cap 5 s, starting at ~1 s after a 503 / close 1012 / `server_restarting`; reset after
  a join that held for 3 s; `Retry-After` is honoured up to 10 s.
* **Final outcomes:** preflight 404 or close `4004` / `room_not_found` / `room_closed` → `room_gone`;
  `4001` / `replaced` → `replaced` (the UI can take the seat back with a new session); `4002` /
  `kicked` → `kicked`; `4003` / `unsupported_version` → reload the app; `4005` / `leave()` → `left`;
  a refused join (anything but `server_busy`, `rate_limited`, `internal`) → `join_rejected`.
  Every other close code (1006, 1001, 1012, 1008, 1009, 1000, unknown) reconnects.
* **Liveness:** `ping` every 20 s; no frame for 45 s drops the socket; `online` and a tab becoming
  visible reconnect immediately (and probe a seemingly live socket with a ping).
* **Answers** are coalesced to ≤ 1 `set_answer` per category per 100 ms, kept (latest text) while
  offline and flushed after the next join unless stale; `basta` flushes first and is never delayed.

**Room route:** a tab that chose the room (`sessionStorage['tf.room.<CODE>']`, set by Home and the gate)
connects at once; any other visit (invite link in a fresh tab) sees the gate (room preflight, nickname,
avatar, Join). Errors become toasts (`err_<code>` messages for every engine/protocol code); closed
sessions get a screen per reason, always with a Home button.

## 10. Assumptions / open tunables

Cluster is amd64. Tunables: English hard-letter list, 120 ms letter speed,
10 s chooser auto-stop, 3–10 category range, 3 s grace, 500 ms late-update
allowance.
