# Tutti Frutti — web client

SvelteKit 2 + Svelte 5 (runes) + TypeScript (strict) + Tailwind CSS v4, built with
`@sveltejs/adapter-static` as a pure client-side SPA (`ssr = false`, fallback
`index.html`) and shipped as an installable PWA. See `../docs/DESIGN.md`.

Status: **Phase 0 skeleton** — placeholder Home screen (nickname, avatar, 4-box
room-code input, ES/EN switch); the buttons show a "coming soon" toast. The real
screens arrive in Phase 3.

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

## Dev proxy

`vite.config.ts` proxies `/api` and `/ws` (WebSocket upgrade enabled) to the Go
server at `http://localhost:8080`. Override with `TF_SERVER_URL=http://host:port npm run dev`.
The server's WebSocket origin check must allow the dev origin `http://localhost:5173`.

## i18n (Paraglide JS)

- Messages live in `messages/es.json` (base locale, Argentine Spanish with voseo:
  "Creá", "Unite") and `messages/en.json`. Keys are flat `snake_case` and must
  exist in **both** files with the same `{parameters}` (a test enforces it).
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
scrolling at 390 × 844 and 360 × 740 (the Google link and ES/EN switch too); the actions are
anchored to the bottom. Keep it that way when you touch the layout.

Room codes (`src/lib/roomCode.ts`): alphabet `BCDFGHJKLMNPQRSTVWXZ23456789`, 4 characters, ASCII-only
upper-casing. Typing/autofill is lenient (invalid characters are dropped); **pasting is strict**:
the text must be a bare code (surrounding whitespace and `-`/space separators are ignored) or an
http(s) invite link whose path ends in a valid code. Anything else (prose, more than 4 valid
characters, `ß`-style Unicode expansions) is ignored and the already typed value is left unchanged.

## PWA

`@vite-pwa/sveltekit`, `registerType: 'prompt'`, Workbox `generateSW`. `UpdatePrompt.svelte`
registers the service worker through `virtual:pwa-register` and shows the localized
"Nueva versión disponible → Recargar" banner. The service worker never answers or caches
`/api/*`, `/ws`, `/healthz`, `/readyz`, `/metrics` (navigation fallback denylist + `NetworkOnly`).
The service worker is disabled in `npm run dev`.

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
location that sets `add_header` (nginx does not inherit headers otherwise). A strict CSP
is planned for Phase 5.
