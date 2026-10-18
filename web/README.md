# Tutti Frutti — web client

SvelteKit 2 + Svelte 5 (runes) + TypeScript (strict) + Tailwind CSS v4, built with
`@sveltejs/adapter-static` as a pure client-side SPA (`ssr = false`, fallback
`index.html`) and shipped as an installable PWA. See `../docs/DESIGN.md`.

Status: **Phase 3** — the whole game UI (Home, invite gate, Lobby, Letter, Playing, Review,
Summary, Final) talks to the Go server over REST + one WebSocket per room. **Phase 4** adds the
account UI (Google sign-in, saved profile, lifetime stats): see "Accounts" below.

## Scripts

| Command                            | What it does                                                            |
| ---------------------------------- | ----------------------------------------------------------------------- |
| `npm run dev`                      | Vite dev server (http://localhost:5173) with the API/WS proxy           |
| `npm run build`                    | Static build in `build/` (+ manifest and service worker)                |
| `npm run preview`                  | Serves the build output (no SPA fallback / SW precache: use Docker)     |
| `npm run check`                    | `svelte-kit sync` + `svelte-check` (compiles the i18n messages first)   |
| `npm run lint`                     | `prettier --check .` + ESLint (typescript-eslint, eslint-plugin-svelte) |
| `npm run format`                   | `prettier --write .`                                                    |
| `npm run test` / `npm run test:ci` | Vitest, run once                                                        |
| `npm run i18n`                     | Compile `messages/*.json` into `src/lib/paraglide/` (git-ignored)       |
| `npm run icons`                    | Re-render the PNG icons / `favicon.ico` from `static/logo.svg`          |

CI order: `npm ci && npm run check && npm run lint && npm run test && npm run build`.
`check`, `test` and `test:ci` run `npm run i18n` first (npm `pre*` hooks), because the
generated Paraglide module is not committed.

## Tests

`npm run test` runs everything (Vitest; component tests use jsdom + Testing Library via
`// @vitest-environment jsdom`). Useful subsets:

```bash
npx vitest run src/lib/game          # network layer: fake WebSocket, fake fetch, fake timers
npx vitest run src/lib/components "src/routes"   # gate, banner, closed screens, Home, room route
```

The network tests (`src/lib/game/*.test.ts`, helpers in `net.testkit.ts`) script a `FakeSocket`, a
routable `fetch` and `vi.useFakeTimers()` with a deterministic jitter; they cover every close code,
the backoff sequence, preflight 404/503/429, heartbeat and dead-connection detection, visibility /
`online` wake-ups, pending-answer flush, rate coalescing, the clock offset, `leave` and `destroy`.
`WsRoomSession` takes injectable dependencies (`createSocket`, `fetch`, `now`, `setTimeout`,
`random`, `profile`, `location`, `win`, `doc`) with browser defaults. Coverage needs
`@vitest/coverage-v8` (not a dependency of this repo): install it outside the tree if you want a report.

## Dev proxy

`vite.config.ts` proxies `/api` and `/ws` (WebSocket upgrade enabled) to the Go
server at `http://localhost:8080`. Override with `TF_SERVER_URL=http://host:port npm run dev`.
The server's WebSocket origin check must allow the dev origin, e.g. (the page may run on any port):

```bash
cd ../server && PORT=8081 PUBLIC_URL=http://localhost:5180 ALLOWED_ORIGINS=http://localhost:5180 go run ./cmd/server
cd ../web && TF_SERVER_URL=http://localhost:8081 npm run dev -- --port 5180
```

An empty `DATABASE_URL` keeps rooms in memory (a server restart loses them, so a reconnecting
client ends on the "room gone" screen); set it to a Postgres to watch games survive a restart
(and set `SESSION_SECRET` to 32+ bytes so guest cookies survive too).

## Architecture

```
src/lib/game/session.ts          RoomSession: the ONLY thing screens know about the network
src/lib/game/client.svelte.ts    WsRoomSession: WebSocket, join, reconnect, heartbeat, clock offset
src/lib/game/backoff.ts          exponential backoff with full jitter
src/lib/game/transport.ts        socket interface, wss URL, frame encode/decode
src/lib/game/roomMark.ts         sessionStorage flag "this tab is in room X"
src/lib/game/fake.svelte.ts      FakeSession: scriptable double for tests and the playground
src/lib/game/fixtures.ts         makeView / makeRound / player / makeSettings
src/lib/api.ts                   REST client + typed ApiError
src/lib/errors.ts                error code -> translated sentence (err_* messages)
src/lib/protocol/*               wire types GENERATED from Go (never edit; `go tool tygo generate`)
src/lib/screens/RoomScreen.svelte  phase -> screen router (Lobby, Letter, Playing, Review, Summary, Final)
src/routes/+page.svelte          Home: create / join
src/routes/[code=roomcode]/      room route: gate -> connecting -> screens (+ banner) -> closed
src/lib/components/{JoinGate,ConnectingScreen,ConnectionBanner,ClosedScreen}.svelte
src/lib/account/                 account store, REST client, GoogleButton, AccountChip (Phase 4)
src/routes/stats/                the stats screen (+ delete-account dialog)
```

### The session contract

Screens receive a `RoomSession` (`src/lib/game/session.ts`) and never touch WebSockets, cookies
or the envelope. Every property is reactive (Svelte 5 runes): `status` (`connecting` |
`connected` | `reconnecting` | `closed`), `closedReason`, `joinError`, `view` (latest snapshot,
kept while reconnecting), `players`, `me`, `isHost`, `isChooser`, `isParticipant`, `events` (last
20), `lastError` (`{code, ref, at, seq}`) and `serverNow()`. Commands never throw: a rejected one
arrives later through `lastError` (`seq` lets a screen react to _new_ errors only). Deadlines in the
view are absolute ISO times: always compare them with `session.serverNow()`, never `Date.now()`.

Two implementations: `WsRoomSession` (real) and `FakeSession` (tests and the playground).

### Connect flow and reconnect rules

Every attempt: `GET /api/session` (once per session; sets the guest cookie) → preflight
`GET /api/rooms/{code}` → open `wss://host/ws` (`ws:` on http) → first frame `join` → the first
`state` means _connected_.

| Situation                                                                         | Result                                                           |
| --------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| preflight 404                                                                     | closed `room_gone` (for good)                                    |
| preflight 429 / 502 / 503 / 504 / network error, socket never opens               | retry forever with backoff (honours `Retry-After`, at most 10 s) |
| close 1006, 1001, 1012, 1008, 1009, 1000 or any unknown code                      | `reconnecting`, last `view` kept, backoff                        |
| close 4001 / event `replaced`                                                     | closed `replaced` (another tab/device took the seat)             |
| close 4002 / event `kicked` for me / error `kicked` on join                       | closed `kicked`                                                  |
| close 4003 / error `unsupported_version`                                          | closed `unsupported_version` (reload the app)                    |
| close 4004 / error `room_not_found`, `room_closed`                                | closed `room_gone`                                               |
| close 4005 / `leave()`                                                            | closed `left`                                                    |
| join refused (`lobby_full`, `invalid_name`, `invalid_avatar`, `wrong_phase`, ...) | closed `join_rejected` + `joinError`                             |
| join answered `server_busy`, `rate_limited`, `internal`                           | retry quietly (no toast per attempt)                             |

- **Never give up on 502/503/504/network**: the server is a single replica and every deploy leaves
  a gap of a few seconds (Traefik answers `503 no available server`).
- **Backoff**: full jitter, ceiling 500 ms doubling up to 5 s; it starts at ~1 s after a 503 / close
  1012 / `server_restarting`. It resets after a join that held for 3 s (a connection that flaps
  right after joining keeps escalating). `status` is `connecting` before the first state and
  `reconnecting` afterwards.
- **Heartbeat**: `ping` every 20 s; nothing at all received for 45 s (10 s before the socket opens)
  closes the socket and reconnects. `online` and `visibilitychange → visible` reconnect immediately
  (backoff reset) when not connected; when connected they send a ping and reconnect if nothing
  answers within 5 s (mobile browsers silently kill background sockets).
- **Clock**: `serverNow() = Date.now() + offset`; the offset comes from `State.serverTime` of the
  join round-trip and from every `pong`, keeping the lowest-latency sample of the last 8.
- **Answers**: `setAnswer` keeps the latest text per category, sends the first change at once and
  coalesces bursts to at most one frame per category per 100 ms (server limit: 20 frames/s, burst
  40). Offline, the latest text is kept and flushed right after the next `state` that follows a
  join — unless it became stale (other round or phase, not a participant) or the server already has it.
  `basta()` flushes pending answers first and is never delayed; every other command while not
  connected sets `lastError = not_connected` and is dropped.
- `destroy()` (called by the route's `onDestroy`) closes the socket and clears every timer and
  listener; it does not leave the room (`leave()` does).

### The room route

`/KT7P` (any case; the matcher is `src/params/roomcode.ts`, the URL is rewritten to upper case):

1. If this **tab** has `sessionStorage['tf.room.KT7P'] = '1'` (set by Home create/join and by the
   gate's Join button) and the profile has a nickname, the session connects straight away
   (reloads included). Otherwise the **JoinGate** checks the room (`GET /api/rooms/{code}`: not
   found / full / game over / server busy states, each with a Home button), asks for nickname +
   avatar and shows a big Join button.
2. `connecting` → a friendly "Conectando…" screen (drawn only after 300 ms, no flash).
3. Once `view` exists, `RoomScreen` renders the phase screen. While `reconnecting` the last screen
   stays and **ConnectionBanner** (polite `aria-live`, never blocks touches) says "Reconectando…".
4. `closed` → **ClosedScreen** per reason (always with a Home button): `left` goes Home directly,
   `replaced` offers to take the seat back (a new session), `unsupported_version` offers Reload,
   `join_rejected` explains why and offers "try again" (back to the gate). The tab flag is cleared
   for `room_gone`, `kicked`, `left` and `join_rejected`.
5. Every new `session.lastError` becomes a translated toast (`err_<code>`, fallback `err_unknown`);
   errors that end the session are not toasted, the closed screen says it.

## Accounts (Phase 4)

Google sign-in is optional: the app is a complete guest app and the account UI appears only when
`GET /api/session` says `googleEnabled`. Design and rules: `../docs/DESIGN.md` → "Accounts UI (Phase 4)".

```
src/lib/account/store.svelte.ts  AccountStore + the `account` singleton: session, user, stats, profile sync
src/lib/account/api.ts           REST calls (/api/session, /api/me, /api/me/profile, /api/me/stats, logout)
src/lib/account/types.ts         re-exports the generated wire types (lib/protocol/api.ts) + Outcome
src/lib/account/returnTo.ts      `/` | `/<ROOMCODE>` allow-list + the login URL
src/lib/account/fake.svelte.ts   FakeAccount: scriptable double for component tests
src/lib/account/account.testkit.ts  routable fake fetch, fake event targets, memory profile
```

- **Using it:** `import { account } from '$lib/account'`; call `account.init()` on mount (idempotent) and read
  `account.status | googleEnabled | loggedIn | user | stats` in templates. `account.login('/')` /
  `account.login('/KT7P')` navigate away to Google; `logout()`, `deleteAccount()` and `loadStats()` return
  `{ ok: true } | { ok: false, code }` and show errors with `accountErrorMessage(code)`.
- **Tests:** logic with a fake `fetch` (`rig()` in `account.testkit.ts`); screens with
  `vi.mock('$lib/account', …)` returning a `FakeAccount` (see `src/routes/stats/page.test.ts`). The store tests
  need `// @vitest-environment jsdom`: in the node environment Svelte compiles for SSR and `$effect` does nothing.
- **Without a server:** in `npm run dev`, intercept the API in the browser (Playwright
  `page.route('**/api/**', …)`) answering `/api/session` `{guest:true, googleEnabled:true, loggedIn:true}`,
  `/api/me` `{id, name, profile}` and `/api/me/stats`; or run the Go server with Google configured
  (`GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`, redirect URI `<PUBLIC_URL>/api/auth/google/callback`).
- **Messages:** `messages/account/{es,en}.json` (`account_*`, `stats_*`; the REST failures are `account_err_*`).

## Dev playground

`npm run dev`, then `http://localhost:5173/dev` lists scenarios; `/dev?s=<scenario>&lang=es|en&bare=1`
renders one room screen with a `FakeSession` and **no server** (only in `vite dev`; a production build
redirects home). Each area registers its scenarios in `src/lib/dev/scenarios.<area>.ts`
(`export const scenarios: Record<string, () => FakeSession>`), picked up automatically.

## i18n (Paraglide JS)

- Messages live in `messages/es.json` (base locale, Argentine Spanish with voseo:
  "Creá", "Unite") and `messages/en.json`. Keys are flat `snake_case` and must
  exist in **both** files with the same `{parameters}` (a test enforces it).
- **Message areas**: besides the core `messages/{es,en}.json`, every feature owns a folder
  (`messages/net/` for the network/Home/gate/closed/error strings, `messages/lobby/`,
  `messages/play/`, `messages/review/`, `messages/account/`) with its own key prefix (`net_`, `err_`, `gate_`, `closed_`,
  `home_` for `net/`). They are listed in `project.inlang/settings.json` (`pathPattern`) and
  merged by Paraglide; a key may exist in ONE area only (`src/lib/messages.test.ts` fails on
  duplicates). English never contains `¡¿ñáéíóúü` and says only "Stop!".
- Use them as `import { m } from '$lib/paraglide/messages'` → `m.create_game()`.
  Call them inside templates or `$derived(...)` so they update when the language changes.
- Locale resolution (no URL prefixes, it is a static SPA): `localStorage` (`tf.locale`)
  → the browser's preferred language (`es*` → `es`, `en*` → `en`) → base locale `es`.
  **A first visit follows the browser language; Spanish (Argentina) is the fallback for
  any other language.** The ES/EN switch overrides it and the choice is remembered. An
  unknown stored value is ignored (falls through to the browser language). Paraglide persists the
  language it resolved on the first visit, so later visits keep it even if the browser language changes
  (only the ES/EN switch changes it). Switch language with
  `i18n.set('en')` from `$lib/i18n.svelte` (it persists the choice, keeps `<html lang>` in
  sync and re-renders without a page reload) — not with Paraglide's `setLocale` directly.
- Compiler options are shared by Vite and the `npm run i18n` script in `paraglide.config.js`;
  the inlang message-format plugin is loaded from `node_modules` (no network needed to build).

## Brand rule

The game is called **Tutti Frutti** in every language. It is the constant `BRAND` in
`src/lib/brand.ts` and must **never** appear as literal text in `messages/*.json`: pass it
as a parameter (`m.share_text({ brand: BRAND, code })`, message `"… {brand} …"`).
The static shell (`src/app.html`, `<title>`/meta, which cannot import `BRAND`) and
`static/logo.svg` (`aria-label`) repeat the literal, and the PWA manifest is built from `BRAND`.
`src/lib/brand.test.ts` scans `src/` and `static/` and fails when any other file contains the
literal (add a file to its allowlist only with a justification) or when an occurrence differs from `BRAND`.

English screens say **"Stop!"** (`basta_button`, `basta_overlay`, `letter_stop`, no Spanish
subtitle); Spanish says **¡BASTA!**, "¡Basta para mí, basta para todos!" and "¡STOP!".

## UI rules

Mobile-first; every interactive element is at least 48 × 48 px (`min-h-12`/`min-w-12`) and
text contrast is at least 4.5:1. Design tokens (fruit palette on a dark "grape night" surface)
are in `src/app.css`; the `tall:` variant (viewport height >= 800px) relaxes the compact layout.

The Home screen is thumb-friendly: logo, nickname, avatar picker (one row of emoji + one row of
colours), **Create game** and the room-code input with its **Join** button all fit without
scrolling at 390 × 844 and 360 × 740 (the account slot — Google button, or My stats + Sign out — and the ES/EN switch too); the actions are
anchored to the bottom. Keep it that way when you touch the layout.

Room codes (`src/lib/roomCode.ts`): alphabet `BCDFGHJKLMNPQRSTVWXZ23456789`, 4 characters, ASCII-only
upper-casing. Typing/autofill is lenient (invalid characters are dropped); **pasting is strict**:
the text must be a bare code (surrounding whitespace and `-`/space separators are ignored) or an
http(s) invite link whose path ends in a valid code. Anything else (prose, more than 4 valid
characters, `ß`-style Unicode expansions) is ignored and the already typed value is left unchanged.

## PWA

`@vite-pwa/sveltekit`, `registerType: 'prompt'`, Workbox `generateSW`. `UpdatePrompt.svelte`
registers the service worker through `virtual:pwa-register` and shows the localized
"Nueva versión disponible → Recargar" banner: a new version waits until the player clicks (no
`clientsClaim`, no automatic `skipWaiting`, so a game is never reloaded by itself).
The service worker never answers or caches `/api/*`, `/ws`, `/healthz`, `/readyz`, `/metrics`
(navigation fallback denylist + `NetworkOnly`). It is disabled in `npm run dev`.

- The manifest lives in `scripts/pwa-manifest.ts` (built from `BRAND`; colours, icons, screenshots and
  the iOS tags are checked by `src/lib/pwa.test.ts`). Colours: `--color-night` = `theme-color` meta =
  `theme_color` = `background_color`.
- Install-dialog screenshots: `static/screenshots/*.png` (540×960, from the dev playground scenarios
  listed in `scripts/screenshots.json`), never precached (`workbox.globIgnores`). Regenerate with
  `npm run dev -- --port 5173` and, in another terminal, `node scripts/screenshots.mjs` (needs a
  Chromium and `playwright-core`, which are not dependencies: see the header of the script).
- Check installability in Chromium: DevTools → Application → Manifest, or CDP
  `Page.getInstallabilityErrors` (must be empty; use a non-incognito profile).
- Test an update locally: build two images with a visible change in between, serve one on a port,
  open the app, replace the container with the other image on the same port and call
  `(await navigator.serviceWorker.getRegistration()).update()`.

## Docker

```bash
docker build -t tutti-frutti-web:dev .
docker run --rm -p 18080:8080 tutti-frutti-web:dev   # http://localhost:18080
```

Multi-stage (`node:22-alpine` → `nginxinc/nginx-unprivileged:stable-alpine`), listens on
**8080** as a non-root user. `nginx.conf` handles gzip, SPA fallback, cache headers
(`no-cache` for `index.html`/`sw.js`/`workbox-*.js`/`manifest.webmanifest`, immutable for
`/_app/immutable/`), `/healthz`, a JSON 404 (`{"error":"not_found"}`) for the whole backend
namespace (`/api`, `/api/...`, `/ws`, `/ws/...`; routes such as `/apix` are still SPA routes),
and includes `nginx-security-headers.conf` in every
location that sets `add_header` (nginx does not inherit headers otherwise).

### Security headers (Phase 5)

Every response carries a strict Content-Security-Policy (no `unsafe-inline`/`unsafe-eval`),
`Permissions-Policy`, COOP/CORP `same-origin`, `X-Frame-Options`, `nosniff`, `Referrer-Policy` and, on
https requests only, HSTS. The policy is in `nginx-security-headers.conf`; the hashes of the shell's
inline bootstrap script and static `style` attributes are generated at build time (`scripts/csp.mjs`, run
by `svelte.config.js` after `adapter-static`, written to `.svelte-kit/nginx/csp-hashes.conf` and copied
into the image). **`npm run build` fails** when the shell or a template gains inline code the policy
would block (an inline `<script>`, an event-handler attribute, a static `style="…"`): use external files
and Svelte's `style:prop={…}` directive (CSSOM, not restricted). `src/lib/security.test.ts` covers the
generator, the policy and the nginx structure. Details, the debugging recipe and the verification:
`../docs/DESIGN.md`, "Security headers and PWA (Phase 5)".

Check a running stack: `curl -sI http://localhost:8000/ | grep -iE 'content-security|permissions|cross-origin'`.
