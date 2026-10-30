# Tutti Frutti — end-to-end tests

[Playwright](https://playwright.dev) (TypeScript) drives the **real stack** — Postgres, the Go server, the
nginx web image and the Caddy edge that stands in for Traefik (`docker-compose.yml` at the repo root) — through
phone-sized Chromium browsers. Several players are several browser contexts (own cookies and storage), so a
test is a conversation between them: create a room, join by code and by invite link, play a round, call
Basta, review, score, rematch.

```
e2e/
  playwright.config.ts   projects, reporters, timeouts
  src/
    env.ts               E2E_* environment variables
    messages.ts          the REAL web/messages/**/{es,en,nl}.json, {param} templates compiled here
    guard.ts             page errors / console errors / failed requests / CSP => the test fails
    fixtures.ts          `test` with an `app` fixture: players, the guard, room-creation pacing
    player.ts            a Player = browser context + tab + typed page objects
    pages/               Home, Gate, Lobby (+ category picker), Letter, Playing, Review, Summary, Final, Closed
    flows.ts             createRoom, joinByCode, joinByInviteLink, startGame, openRound, chooserStops(After), bastaBy, askToStop, quickRound
    scenarios/           the reference game (run in es, en and nl)
    no-spanish.ts        "nothing Spanish on this screen" check for the en and nl screens
    network.ts           drop / restore a player's connection
    compose.ts           restart the server container
    bots.ts              browserless players over WebSocket, generated protocol version (currently 5), snapshots and heartbeat
    rooms.ts             paced REST creation, cookie-preserving host sockets and public directory requests
    a11y.ts              axe helper: serious/critical + two moderate rules fail, exactly one `h1` per screen, an (empty) allow-list for known findings
    rules.ts             alphabets, hard letters, default categories (es, en, nl), the Basta words, the 15 s vote as DESIGN.md states them
    layout.ts            `expectUnobscured`: a control is wholly on screen and nothing lies over it (phone-size checks)
    terms.ts             accepts the terms for a player / bot (GET /api/session, POST /api/terms/accept, same cookie jar)
    random.ts            seeded PRNG (E2E_SEED)
  scripts/run-local.sh   `npm run test:stack`
  tests/                 the specs (below)
```

## Run it

You need Node 24 (22.22.2+ works) and Docker. **One command** (builds and starts the stack, runs everything, tears it down):

```bash
cd e2e && npm ci && npx playwright install chromium && npm run test:stack
```

(`npm run test:stack -- -g "kicked" --headed` passes arguments on to Playwright; `KEEP_STACK=1` leaves the stack
running; `EDGE_PORT`, `POSTGRES_PORT`, `E2E_COMPOSE_PROJECT` and `COMPOSE_FILE` move it elsewhere.) Or by hand, from the repo root:

```bash
docker compose up -d --build --wait          # the stack on http://localhost:8000
cd e2e
npm ci
npx playwright install chromium              # first time (add --with-deps on a bare Linux box)
E2E_COMPOSE_PROJECT=tutti-frutti npm test    # everything, ~3 minutes
```

(`docker compose down -v` when done.) A second stack next to yours, on other ports and under another project
name, works the same way: `EDGE_PORT=18400 POSTGRES_PORT=15400 docker compose -p tf-e2e up -d --build --wait`, then
`E2E_BASE_URL=http://localhost:18400 E2E_COMPOSE_PROJECT=tf-e2e npm test`.

| Command                                                  | What                                                        |
| -------------------------------------------------------- | ----------------------------------------------------------- |
| `npm run test:stack`                                     | build + start the stack, run everything, tear it down       |
| `npm test`                                               | all projects (against a stack that is already up)           |
| `npm run test:fast`                                      | the `chromium` project only (~2 minutes)                    |
| `npm run test:slow`                                      | the 60 s time-limit round only                              |
| `npx playwright test tests/03-custom-categories.spec.ts` | one spec                                                    |
| `npx playwright test -g "kicked"`                        | by title                                                    |
| `npx playwright test --ui` / `--headed`                  | watch it                                                    |
| `npm run report`                                         | open the last HTML report                                   |
| `npm run lint`                                           | prettier + eslint (with `eslint-plugin-playwright`) + `tsc` |

### Environment

| Variable              | Default                 | Meaning                                                                                                                             |
| --------------------- | ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `E2E_BASE_URL`        | `http://localhost:8000` | where the app is                                                                                                                    |
| `E2E_COMPOSE_PROJECT` | _(unset)_               | compose project of the stack; needed by the server-restart test, which is **skipped** without it (e.g. against a remote deployment) |
| `E2E_WEB_DIR`         | `../web`                | the web sources whose `messages/` the suite reads; point it at the checkout the stack was built from                                |
| `E2E_WORKERS`         | `2`                     | parallel workers (see rate limits)                                                                                                  |
| `E2E_SEED`            | time                    | seed of the random letter-tap delays of the hard-letters spec (printed as a test annotation)                                        |
| `E2E_A11Y_VERBOSE`    | _(unset)_               | also print the non-blocking axe findings (every moderate/minor one except the two blocking rules, see `09-accessibility`)           |
| `CI`                  |                         | enables one retry, the GitHub reporter and `forbidOnly`                                                                             |

## Projects

- **`chromium`** — Pixel 7 emulation at 390×844, every spec except the next two. The default fast loop.
- **`slow`** — `*.slow.spec.ts`: waits for the _server's_ clock (a 60 s round time limit, the shortest allowed; the 15 s of a vote to stop; public fallback's 20–30 s / 40–55 s fills).
- **`restart`** — `*.restart.spec.ts`: restarts the server container (`docker compose -p $E2E_COMPOSE_PROJECT restart
server`). Every other test would notice, so it runs **alone, after the other two** (project dependencies; if
  one of them fails it does not run — fix that first, or `--project=restart --no-deps`).
- **`deploy`** — `*-update-deploy.restart.spec.ts`: swaps the service worker script inside the web container (`18-update-deploy`).
  It is a project of its own that runs **after `restart`**: specs of one project run in parallel workers, and the 502s of
  a restarting server would reach the browsers of a deploy test (and the guard fails on them). Every spec that changes a
  container gets its own project in this chain. Run it alone with `--project=deploy --no-deps`.

Traces are kept for failures (`trace: 'retain-on-failure'`); there is no video.

## The specs

| Spec                        | What it proves                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `00-safety-nets`            | The safety nets catch what they are for: the guard reports a page error, a console error, a failed request, a 404 and a CSP message (and an allowance silences exactly what it names); the English-only and Dutch-only checks (`src/no-spanish.ts`) fail on a Spanish screen, catch every kind of leak (`¡ ¿ ñ`, "basta", a wrong `<html lang>`) and let Dutch `ë é ï` pass; the accessibility check fails on a nameless button (serious), on a control whose name lacks its visible text and on a screen without `h1` (the two moderate rules it enforces) but only reports another moderate finding, and a `KNOWN_VIOLATIONS` allowance silences exactly the element it names; the vote-to-stop checks (`expectNoVote`, `expectTally`, the prompt/onlooker/dialog locators, `player.sentCount`, `expectUnobscured`) fail when the vote, the prompt, the dialog, the frame or the visibility is absent.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `01-game-es` / `02-game-en` | The reference game (`src/scenarios/reference-game.ts`), 3 players: create; join by code and by invite link (lowercase code canonicalised); settings visible to guests; start; the chooser is the host, then the next player in join order; fill; hidden answers; Basta by one (overlay `¡Basta para mí, basta para todos!` / `Stop!` for the others); review (host walks all categories, two guests reject an answer, badges move for everyone); totals equal a hand-computed table; round 2; "finish early" confirmation; last-round summary; podium; rematch; leave; the second game starts from zero. English: the same, and **no Spanish character or word on any screen** (Dutch: `10-game-nl`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `03-custom-categories`      | 2 built-in + 1 custom (✏️) category, duplicates by case/accents/spaces and against built-in names in both languages are refused ("Ya existe: …"), minimum 3, guests see the same fields, the custom field is played and reviewed, a rematch keeps it.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `04-time-limit.slow`        | A 60 s round: allowed values, the clock counts down on both screens, the 10 s warning, "time's up" overlay without a caller, answers typed before the deadline are scored; with a field missing the button offers the vote and never calls Basta.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `05-hard-letters`           | The hard-letter switch and the language change the letter list and the rounds limit (27/26/26 letters in es/en/nl; 20/20/23 with the hard ones out, rounds clamped; the Dutch lobby from a Dutch screen: `Eruit: Q X Y · 23 letters in het spel`, `Naam, Land, Dier, Eten, Kleur, Merk`), and several quick rounds (es, en and nl lobbies) never reveal a hard letter, never repeat a letter and list the letters already played.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `06-reconnection`           | Reload mid-round (same round, answers restored from the server) and in the review/summary; a dropped network (banner, offline typing, Basta waits, answers arrive after the reconnect); a dropped lobby socket keeps the seat.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `06-server-restart.restart` | The server container restarts mid-round and again in the review: banner, then both players are back in the same room, round, letter, seats and answers; scores are the same as without restarts.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `07-gate-and-closed`        | Unknown code (link and Home), full lobby of 10 (nine WebSocket bots), a kicked player (closed screen, ban holds), a mid-round joiner waits for the next round and then plays it, a second tab replaces the first (and can take the seat back), a finished game cannot be joined.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `08-pwa`                    | The service worker registers and activates on the first visit, no update prompt on a fresh load, an offline reload of `/` shows the shell and the offline banner, the manifest is installable.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `09-accessibility`          | axe-core on **every screen** in es, en and nl: Home, the invite gate (and "no such room"), the lobby (host with a time limit chosen, guest) with its QR / confirm-removal / leave sheets and the category picker with its custom field, the letter screen (chooser, waiting, late joiner), playing (clock running, a watcher, an empty and a wrong-letter field), the review (before and after a "same" proposal and a rejection; host, guest, spectator), summary and final, the vote to stop (confirmation, voter's prompt before and after a yes, the asker's banner), the closed screens (replaced by another tab, removed from the room), the offline banner and the account screens (guest, signed in, no games yet, the delete dialog; `/api/*` is faked with the service worker blocked). **Nothing is tolerated:** no `serious`/`critical` violation and none of the moderate rules `page-has-heading-one` and `label-content-name-mismatch` (WCAG 2.5.3); `KNOWN_VIOLATIONS` in `src/a11y.ts` is empty. The Basta overlay (0.9 s) and the reconnecting pill are not reachable in a browser run; the playground sweep `web/src/lib/a11y.test.ts` covers them.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `10-game-nl`                | The Dutch game: the reference game in Dutch (a Dutch screen makes a Dutch lobby, `Naam Land Dier Eten Kleur Merk`, `Stop!` on the button and the overlay, **nothing Spanish on any screen**), with the third category written `Lĳst` / `Lijst` / `LIJST` (the ligature ĳ is "ij": one answer, 5 points each); and a round whose letter is **I** (the chooser taps in I's 120 ms slot, timed inside the page, and re-aims after a miss): `Ysland` is flagged "must start with I", `Ĳsland` / `IJsland` / `ĳsvogel` / `Ijs` are valid, shown as typed and shared (5 points).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `11-browser-language`       | First visit: a `nl-NL` / `nl-BE` browser gets Dutch, `en-GB` English, `es-ES` and `fr-FR` Spanish (the switch shows it, nothing Spanish on the Dutch/English Home); a language chosen with ES / EN / NL beats the browser language and survives a reload, a new tab and a later visit.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `12-stop-vote`              | The vote to stop (host switch `Permitir pedir que paren`, on by default). **es:** (a) with a category empty the button stays enabled and opens a confirmation (`alertdialog`, focus on "keep playing", Escape closes it and `ask_stop` is never sent), confirmed: the asker's banner "0 de 2", the voters' non-modal prompt (typing in a field while it is open), yes by yes "1 de 2" / "waiting", the last yes shows the Basta overlay with the asker's name, lock, review, the asker's empty cell has no points, hand-computed totals (the asker sent no `basta`); (b) a no rejects at once (only the asker gets the toast, nobody is named), she asks again right away (no cooldown) and it passes; (d) switched off: the guests' chip says so and the button is **disabled** until everything is valid; (e) somebody with everything valid calls Basta while the vote is open: the prompts vanish, the overlay names that player, no toast for the asker; (f) a voter going offline / closing the tab leaves the electorate (the others' yes passes it; live "0 de 1"), the asker going offline cancels it (and a new vote starts from zero); (g) a spectator sees the onlooker line, has no buttons, and a bot spectator's `vote_stop` is refused with `not_participant` and does not count. **en, nl:** (h) the whole vote (switch, confirmation, prompt, rejection, second vote, overlay, review) with the exact words, `Stop!` and **no Spanish on any screen**. **360×740, Dutch, a 16-letter name:** the focused field is wholly visible (inside the screen, nothing over it) with the prompt open, for every field, and the buttons are ≥ 48 px. |
| `12-stop-vote-timeout.slow` | 15 s without unanimity (one yes, one silence) reject the vote: the toast (es `Se acabó el tiempo`, en `Time ran out`, nl `De stemtijd is om`) only for the asker after about 15 s, prompts and banner vanish, the round goes on and she can ask again. Real time: no clock can be moved in the stack.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |

### Public discovery and fallback

`15-public-rooms.spec.ts` adds six tests, one API/WS and one browser flow per language (es/en/nl):

- An unclaimed REST-created public room stays absent until its host successfully joins over WebSocket.
- The directory returns the public lobby's settings, filters by language, excludes private rooms and drops
  a room once the game starts. Public creation defaults to 90 seconds; omitted visibility stays private
  with no time limit.
- Public creation through Home, `/rooms` discovery, the fresh visitor's nickname gate and join all work;
  the casual caption appears and an explicit private room still works through its invite link.

`16-public-fallback.slow.spec.ts` adds four tests:

- In every language, wait for the **production** first fill (20–30 seconds, with a one-second transport
  tolerance) and second fill (40–55 seconds), then complete a two-round game. The WS host shortens the
  phases using ordinary commands. Automated seats make answer progress while answers remain hidden,
  do not enter stop/reject electorates, and take their chooser turn. The host finishes in a real browser,
  where the final screen shows the casual caption and no automated-player labels. Wire player objects
  have ordinary seat fields only.
- An early human arrival cancels pending fill past both deadlines; later human arrivals prune two
  existing automated seats down to one and then zero.

`17-public-accessibility.spec.ts` adds three locale cases at **360×740**: empty and populated
discovery, axe and exactly one heading, no horizontal overflow, no Spanish on EN/NL screens,
48 px unobscured controls, public-create preselection, and Home without vertical scrolling.
It fetches real directory data and projects it onto the fixture's code for deterministic isolation.

**Terms and safety.** The server answers `terms_required` to a guest that has not accepted the terms, so every
player of `app.player(...)`, `createRoomAPI`, `joinRoomSocket` and every `Bot` accepts them first (`src/terms.ts`);
`app.player(name, { acceptTerms: false })` is a first visit. `19-terms-gate.spec.ts` covers the gate (create, join by
code and by invite link, Not now, persistence, a client that wrongly believes it accepted, on REST and on the
WebSocket), `20-reports.spec.ts` reports (an answer and a nickname answered `201`, a failed send kept as a draft and
retried with the same idempotency key, `429`) and `21-blocks.spec.ts` "Block and leave" and the block list.

**Android batch.** `22-resume-recent-room.spec.ts` simulates a killed browser process with `relaunchBrowser(app, player)`
(`src/flows.ts`: the old context is closed and a new one starts from its `storageState`, so the cookies and localStorage come
back and sessionStorage does not; `app.player(name, { storageState })`) and checks "Resume recent game" on Home: the same seat
in a lobby and in a running round, the card gone for a room that is gone, another instance id, an expired or corrupt entry, and
Home fitting 360×740. `23-android-context.spec.ts` defines `document.referrer` as `android-app://xyz.aresius.tuttifrutti` with an
init script on the first document of a tab (a navigation cannot produce it) and checks that the support links disappear from
Home and Final while Privacy stays, that the marker survives Back from a static page and a reload, and that it is per tab.

Run against the already-built stack with bots enabled and default production timings:

```bash
cd e2e
E2E_BASE_URL=http://localhost:8000 npx playwright test tests/15-public-rooms.spec.ts tests/16-public-fallback.slow.spec.ts
```

Budget **about 4–6 minutes** for these ten tests (the four slow tests share a spec and run sequentially).
Each fallback game has a 180-second ceiling; the arrival test has a 120-second ceiling. Waits use
Playwright assertions over received snapshots and normal 20-second protocol heartbeats: no fixed sleeps,
clock manipulation, or accelerated server fill configuration. The directory-only tests can run with
`--project=chromium -g 'directory lists'`; authoring checks are `npm run lint` and `npx playwright test --list`.
Authenticated lifetime-stat persistence remains covered by the server tests; these guest E2E cases verify
the public casual presentation.

## How the tests are written

- **Labels come from the app.** `msg(locale, key, params)` (`locale` is `es`, `en` or `nl`) reads `web/messages/**` (every area folder), so a
  reworded button never leaves a stale test; an unknown key throws. The exact words of the Basta call (the button `¡BASTA!` / `Stop!`
  and the overlay `¡Basta para mí, basta para todos!` / `Stop!`, the latter in English and in Dutch) and the rules (alphabets, hard letters, scoring) are written out in the tests on
  purpose: if the app drifts from `docs/DESIGN.md`, a test must notice.
- **Selectors** are `getByRole` / `getByLabel` with exact accessible names. Existing test ids are used where they
  exist (`podium`, `category-title`, `points-badge`, `answer`, `grace-banner`, `basta-splash`, `data-player`,
  `data-rank`, `stop-wait-banner`, `stop-vote-sheet`, `stop-vote-onlooker`); a few lookups fall back on structure (see "Test ids that would help").
- **No fixed sleeps.** Every wait is an auto-waiting assertion or `expect.poll`. The only deliberate delays: the
  room-creation pacer, the random tap delay of the hard-letters spec (the server derives the letter from
  _when_ the chooser taps, so a prompt tap always yields the first letters) and the _aimed_ tap of `10-game-nl`,
  which needs the letter I: `letter.watchArrival()` before the round, then `chooserStopsAfter(everyone, ms)`
  taps `ms` after the letter screen appeared, timed inside the page (every letter lasts only 120 ms; a miss
  plays a quick round and re-aims from the letter that came up).
- **The guard** (`src/guard.ts`) fails every test on an uncaught page error, a console `error`, a failed or `>= 400`
  same-origin request, and anything mentioning _Content Security Policy_ / _Refused to …_. A test that provokes one
  on purpose says so with `app.guard.allow({ … why })` (404 of an unknown room, the offline window, the server
  restart). The CSP check is never allowable.
- **Phone-size reality** (`src/layout.ts`): `expectUnobscured(locator, what)` is true only when the control is wholly
  inside the screen and `elementFromPoint` finds the control itself at five points of it (so a sticky footer, a
  header or a prompt lying over it fails); `12-stop-vote` uses it at 360×740 (`PlayerOptions.viewport`).
- **Short-lived UI is recorded, not raced:** the Basta overlay lasts 0.9 s, so `watchSplash()` installs a
  `MutationObserver` before the call and the test reads what it saw.
- **Frames sent are counted:** `player.sentCount('ask_stop')` says how many frames of that type the player's tab has
  put on the room socket (`ask_stop`, `vote_stop`, `basta`), which is how "Escape asked nobody" and "the vote passed,
  the asker sent no Basta" are proven without guessing from the screen.
- **Dropping the network** needs more than `context.setOffline(true)`, which leaves an open WebSocket alone:
  `Network.drop()` also closes the page's room socket (a tracked-WebSocket init script), which is what the server
  sees when a phone loses its connection.

### Rate limits

The server limits (per client address, in practice **one global bucket** because every client has the same address
behind the edge, in the compose stack as in the cluster) are: room creation 30/min with a burst of 10, room lookups and
WebSocket upgrades 300/min with a burst of 60 each. `TRUST_FORWARDED_FOR` is off in compose, so a per-test
`X-Forwarded-For` would not help. The suite creates about 25 rooms and a few hundred lookups in 3 minutes; each worker
also mirrors its share of the creation bucket (`RoomCreationPacer` in `src/fixtures.ts`) and waits before a creation
that would overdraw it, so a 429 can never flake a run. Keep `E2E_WORKERS` small, and do not run two suites against
the same stack at once.

### Adding a test

1. Import `test`/`expect` from `src/fixtures`; get players with `await app.player('Ana', { locale: 'es' })` (`locale` is
   the screen language the player expects: `es`, `en` or `nl`, which is also the language of the browser unless
   `browserLanguage: 'fr-FR'` says otherwise; add `network: true` if you will drop their connection).
2. Use the flows (`createRoom(app, host)`, `joinByCode`, `joinByInviteLink`, `startGame`, `chooserStops`,
   `bastaBy`, `quickRound`) and the page objects (`ana.lobby`, `ana.playing`, `ana.review`, …). New screen or
   control → a method/locator on the page object, not a selector in the test.
3. Name answers with `answerFor(letter, player, field)` / `uniqueAnswers(...)`: they start with the round's letter,
   and are unique per player and field, so the points are predictable (10 unique, 5 shared, 0 empty/rejected).
4. Expected noise (a 404 you provoke, a server you restart) goes through `app.guard.allow`; anything else the
   guard reports is a bug of the app or of the test.
5. A spec that must wait for the server clock → `name.slow.spec.ts`; one that restarts containers →
   `name.restart.spec.ts`. Keep rooms independent (never rely on a code another test created).

### Debugging

```bash
npx playwright test -g "kicked" --headed            # watch
npx playwright test --ui                             # time travel
npx playwright show-trace test-results/<test>/trace.zip
npx playwright show-report                           # HTML report
docker compose logs --no-color --tail=200 server     # what the server saw
```

A failing test writes `error-context.md` (a page snapshot at the moment of failure) next to its trace. In CI the
report, traces and the compose logs are uploaded/printed when the job fails.

## Deliberately not covered

- **Real Google login** (needs real credentials and a browser consent screen; the redirect and cookie contract is
  covered by the server's own tests).
- Other browsers (Firefox, WebKit) and desktop sizes: the app is mobile-first and the suite runs one phone profile.
- Load and soak tests; the Kubernetes/Traefik specifics (the Caddy edge only mimics the path routing).
- Visual regression (screenshots are for humans, see the traces).

## Test ids that would help

None are required; these would replace structural fallbacks: `data-testid="category-chips"` on the selected-category
list of the lobby settings (now `section ol > li`), and a stable role/label for the lobby player rows (now the
`Jugadores`/`Players` region's list items, matched by their text).
