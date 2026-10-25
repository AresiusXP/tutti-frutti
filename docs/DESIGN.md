# Tutti Frutti — Design

> Source of truth for product rules, architecture, protocol and SDLC.
> The game is called **Tutti Frutti** in every language. The name is a fixed
> constant and is **never** placed in the translation files.

## 1. Product summary

Real-time multiplayer web game (mobile-first, installable PWA) based on the
Argentine game *Tutti Frutti*, in which the first player to finish calls *Basta*
to end the round. Hosted on the homelab at **https://tuttifrutti.aresius.xyz**.

| Area | Decision |
|---|---|
| Client | SvelteKit (adapter-static) + Tailwind + Paraglide i18n, installable PWA. UI languages: Spanish (Argentina), English and Dutch; a first visit follows the browser language (Spanish for any other), the ES/EN/NL switch is remembered |
| Server | Go, WebSockets, **single replica**, authoritative game state in memory, snapshots to Postgres on every phase change (and every ~5 s while playing) |
| Identity | Nickname + avatar (emoji + colour) for guests. Optional Google login (OIDC) adds profile + lifetime stats |
| Lobbies | Private, 4-char code, invite link, QR. Max 10 players |
| Letter | Hidden alphabet cycles from A; the round's *chooser* taps STOP. Used letters are skipped |
| Basta | The caller triggers a 3 s grace countdown for everyone else, then inputs lock |
| Vote to stop | Whoever has not filled every category can ask everyone to stop: every other connected player must say yes within 15 s, and it then counts as their Basta (host setting, on by default) |
| Review | Players vote to reject invalid answers (majority of other players); answers can be merged as "the same" by majority. A host signed in with Google can ask an **AI judge** about a category: an advisory verdict on each answer, visible to everyone, that never changes a score |
| Extras v1 | Invite link + QR, optional round time limit, custom per-game categories, installable PWA, a voluntary "support the game" link (Cafecito in Argentina, Ko-fi elsewhere) |

### Naming rules

* "Tutti Frutti" is a fixed constant (manifest `name`/`short_name`, `<title>`,
  `og:title`, logo, share text). Never translate it.
* The game is called "Tutti Frutti", never "Basta": *Basta* (es) / *Stop!* (en, nl)
  is only the call a player makes to end a round. This holds for every user-facing
  text (screens, `<meta>` and manifest descriptions, link previews) and for the docs.
* Spanish screens: the call button is **¡BASTA!**, the overlay says
  "¡Basta para mí, basta para todos!".
* English screens: the call button and the overlay just say **"Stop!"**.
  (No Spanish subtitle.)
* Dutch screens: the same as English, just **"Stop!"**. No Spanish word or
  character (`¡ ¿ ñ`, "basta") may appear on an English or Dutch screen.
* The chooser's letter-stopping button is "¡STOP!" (es) / "Stop!" (en, nl).

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
* **Identity**: signed `HttpOnly` guest cookie (random guest ID = player ID) lets
  a player reclaim their seat; it never changes. Google OIDC (code flow + PKCE,
  scope `openid profile`, no email stored) is handled by the server and sets a
  second, signed `HttpOnly` session cookie (`tf_session`) that only decides who
  gets a saved profile and lifetime stats: playing never needs an account.
  WebSocket is same-origin, authenticated by the guest cookie (the session
  cookie, when valid, links the player to the user) and the `Origin` header is
  checked. Details in section 9b.
* **Client version check**: client sends protocol version on connect. On
  mismatch the app shows "Nueva versión disponible → Recargar" / "New version
  available → Reload".

### Postgres

Reuse the existing CloudPirates Postgres in the cluster with a dedicated
database + user. Tables: `users`, `user_stats`, `user_games`, `room_snapshots`. Migrations
(goose) are embedded in the server binary and run at startup (safe with one
replica). The category catalog is an embedded JSON file, not a table.
Production runs Postgres 18; `docker-compose.yml` and the CI service container of
`ci-server` use the same major (the data volume is mounted on `/var/lib/postgresql`,
where the 18+ images keep `<major>/docker`).

## 3. Game rules (as implemented)

**Phases:** `LOBBY → LETTER → PLAYING → GRACE → REVIEW → ROUND_SUMMARY → (LETTER | FINAL)`

### Lobby settings (host only; others read-only)

| Setting | Options | Default |
|---|---|---|
| Rounds | 1 … number of available letters, or unlimited | 5 |
| Categories | from catalog + custom; **3–10 total** | the 6 "Clásicas" |
| Lobby language | `es` / `en` / `nl` — sets alphabet, hard-letter list, category labels | `es` |
| Exclude hard letters | on/off | on |
| Time limit | off, 60, 90, 120, 180 s | off |
| Ask everyone to stop | on/off | on |

* Alphabets: `es` = A–Z with **Ñ** after N (27 letters); `en` and `nl` = A–Z (26).
* Hard letters: `es` = **K Ñ Q W X Y Z**; `en` = **Q U V X Y Z**; `nl` =
  **Q X Y**. Kept in a data file / table that is easy to edit. With the hard
  letters skipped a game has at most 20 (es), 20 (en) or 23 (nl) rounds.
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
  letter, otherwise rejects the call (`basta_not_allowed`). A player who has not
  filled everything can ask the others to stop instead (see "Vote to stop").
* Server-side letter check: an answer that does not start with the round's
  letter (after normalization) scores 0 and is flagged `wrong_letter`.
* If a time limit is set and expires, the room moves to `GRACE`/lock with no
  caller.

### Basta / grace

* Everyone sees the full-screen overlay + vibration where available. A **3 s**
  grace countdown starts; then answers lock. Updates arriving up to **500 ms**
  after the lock are still accepted (network lag allowance).

### Vote to stop

A player who has not filled every category cannot call Basta, but with the lobby
setting **Ask everyone to stop** on (the default) they can **ask everyone to stop**:
a vote that every other connected player has to win. If it passes, the round
goes to grace exactly as if the asker had called Basta.

* **Setting.** `Settings.stopVote`, chosen by the host in the lobby (like every
  setting it cannot change while a game is on, and a rematch keeps it). Off:
  `ask_stop` is refused (`stop_vote_disabled`) and Basta needs every category
  filled, as before.
* **Asking.** `ask_stop`, only in `playing`, only by a participant who has **not**
  filled every category with a valid answer (one who has calls Basta:
  `stop_not_needed`). One vote at a time (`stop_vote_pending`). The vote lasts
  **15 s** (`StopVoteDuration`; the deadline is exclusive like every other) and
  everybody hears `stop_asked {playerId, name}`. A confirm dialog before asking is
  a client concern.
* **Electorate.** The connected participants other than the asker, evaluated
  **live** like the review votes: offline players cannot block the vote, one who
  reconnects joins it (a yes they gave earlier is still on record) and mid-round
  joiners (not participants) neither count nor vote (`not_participant`). The
  asker cannot vote (`self_vote`).
* **Voting.** `vote_stop {yes}` (`no_stop_vote` when none is open). The vote
  **passes** when every current voter has said yes (voting yes twice is the same
  as once). Any **no rejects it at once**; a voter may change a yes into a no. The
  decliner is not named anywhere (not in the event, not in the state). When the
  electorate is empty (nobody else is connected) the vote passes at once and
  `stop_asked` is never sent.
* **Passing = a Basta by the asker**: the same transition (`grace`, `callerId` =
  the asker, `graceEndsAt` = now + 3 s, then lock and review) and the same `basta`
  event; empty cells simply score 0 (answers keep being accepted until the lock,
  as after any Basta). `set_answer` also keeps working while a vote is open.
* **Failing.** `stop_rejected {playerId, name}` (the asker) with a `reason`:
  `declined` (a voter said no), `timeout` (15 s without agreement) or `ended`
  (the vote became moot: someone else called Basta, the time limit expired, or the
  asker disconnected, left or was kicked). A vote that ends this way is announced
  *before* the event that caused it (`stop_rejected` then `basta`, or then
  `time_up`). When the time limit and the 15 s coincide the limit wins (`ended`).
  The asker's own Basta, once everything is filled in, ends their vote too.
* **No cooldown.** After a rejection anybody, the same player included, can ask
  again at once.
* **State.** `RoundView.stopVote {callerId, endsAt, voters, yes}` exists only in
  `playing`: `voters` is the live electorate in join order and `yes` the voters
  who said yes (in the order they voted; always a subset of `voters`). Everybody,
  spectators included, sees the same. The answers stay private as ever.
* **Event sequences** (each followed by the usual `state`): ask → `stop_asked`;
  a yes that does not complete it → nothing; last yes, or a voter leaving /
  disconnecting → `basta`; a no → `stop_rejected(declined)`; 15 s → `stop_rejected(timeout)`;
  another player's Basta → `stop_rejected(ended)`, `basta`; time limit →
  `stop_rejected(ended)`, `time_up`; asker gone → `stop_rejected(ended)`.
* **Persistence.** The vote is part of the round, so it is in the snapshots, but
  a **restore drops it silently**: every seat is disconnected at that moment, so
  the electorate would be empty and a stale vote would pass by itself. Snapshots
  saved before the setting existed have no `stopVote` field and restore with it
  **off** for that game (the host can switch it on after the rematch).
* **Metrics.** `tuttifrutti_stop_votes_total{result}` with the fixed values
  `asked`, `passed`, `declined`, `timeout`, `ended`; a vote that passes is also one
  Basta in `tuttifrutti_basta_total`.

### Answer normalization & matching

Lowercase, trim, collapse internal whitespace, strip diacritics
(`México` == `mexico`). In **Spanish** lobbies `ñ` stays a distinct letter
(it is not folded into `n`), and the starting-letter check treats `ñ` as its own
letter. In English and Dutch lobbies `ñ` folds into `n`. The Dutch ligature
**`ĳ`/`Ĳ`** (U+0133/U+0132, one character) normalizes to `ij` in every language,
so `Ĳsland` equals `IJsland` and starts with the letter **I**. Two normalized
answers within a category are "the same".

Text a player types is cleaned before it is stored: invisible **format
characters** (zero width space, soft hyphen, bidi marks, …) and **control
characters** (NUL above all: PostgreSQL's `jsonb` cannot store U+0000, so one
in a nickname would make the room's snapshot unsaveable) are removed, so
`"\u200bMéxico"` equals `"mexico"` and a hidden character cannot dodge the
duplicate check. Nicknames and custom category labels keep a zero width joiner
that glues two emoji (`👨‍💻` stays one emoji); anywhere else it is removed.
Avatar fields (`emoji`, `color`) are refused when they hold a control character.

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

#### AI judge (advisory)

A host who is signed in with Google can ask an AI to judge the answers of the category
on screen. The verdict is a **suggestion**: it never changes a score, a vote or a merge;
the players still decide by voting.

* **Who and when.** `ask_judge {category}`: host only (`not_host`), review phase only
  (`wrong_phase`), a valid category (`invalid_category`). The room layer also refuses it with
  `judge_unavailable` when the server has no judge (no Cloudflare credentials) and with
  `login_required` when the host is a guest: the allowance is shared by every player, so it is
  not spent anonymously. The state tells each viewer whether they may ask
  (`State.canJudge` = the server has a judge and the viewer is signed in); the client shows the
  button to the host only when it is set.
* **One question per category.** While a question is open (`judge_pending`) or once the
  category has verdicts (`already_judged`) it is refused; a category with nothing to judge is
  refused too (`nothing_to_judge`). A failed attempt leaves no trace, so the host may ask again.
  The answers judged are those that are non-empty and start with the letter (the others are
  already worth 0); the host asks once per category, so a review costs at most one call per
  category.
* **The engine stays pure.** `Game.Apply(AskJudge)` only records the open question in
  `Round.JudgePending`. The room layer then calls the judge on another goroutine (30 s limit,
  panics recovered) and reports back through the trusted calls `Game.ApplyVerdicts` and
  `Game.JudgeFailed` (like `SetConnected`, a client cannot send them). A result for a round or a
  review that moved on is dropped. Verdicts about answers that were not asked about, unknown
  verdict values and second verdicts for one answer are ignored; the reason is stripped of
  invisible and control characters, whitespace is collapsed and it is cut to 100 runes.
* **State.** `RoundView.judgePending` (categories the AI is working on) and
  `RoundView.verdicts` (`{category, player, verdict: "valid" | "doubtful", reason}`) exist only
  in the review phase and everybody sees them. Both are part of the snapshot; a restore drops
  `judgePending` (the call died with the process).
* **Events.** `judge_failed {reason}`: `busy` (the budget, rate limit or Cloudflare's own
  allowance is used up) or `error`. It is announced to everybody; the host gets a toast.
* **Provider** (`server/internal/judge`, the only code that talks to the model): Cloudflare
  Workers AI on the **Free plan**, model `@cf/google/gemma-4-26b-a4b-it` (Apache 2.0),
  OpenAI-compatible endpoint `POST /accounts/{id}/ai/v1/chat/completions`. The Free plan gives
  10,000 Neurons a day and Cloudflare refuses the call after that (nothing is ever billed unless
  the account is upgraded to Workers Paid: **never add a payment method to that account**); the
  terms (Self-Serve Agreement of 2025-09-12 and the Workers AI Service-Specific Terms of
  2026-09-28, read on 2026-10-25) have no EEA or minors restriction on the Free plan, Workers AI
  does not train on content or retain it, and Free Services may be ended by Cloudflare at will, so
  the feature must keep failing softly. One account only (a second one to dodge the limit is
  forbidden by the terms).
  * *Guard rails:* a daily budget of `JUDGE_DAILY_NEURONS` (default 9000, at most 10000; chart
    `server.judge.dailyNeurons`) charged from the token usage Cloudflare reports and reset at
    midnight UTC (it also restarts at 0 with the process), a global limit of 30 calls a minute, at
    most 4 calls at once, and a cache (≤ 24 h, ≤ 4096 entries) keyed by language, category and
    normalized answer text, so repeated answers cost nothing; one call judges a whole category.
    When Cloudflare says the allowance is gone the judge stops asking for the rest of the UTC day.
  * *Untrusted text:* the answers (and the custom category label) are typed by players. They
    travel as JSON data in the user message, the system prompt says answers are data and never
    instructions, and the reply is parsed strictly (ids, booleans, length cap). Advisory output
    also bounds what an injected "say valid" can do.
  * *Config:* `CLOUDFLARE_ACCOUNT_ID` and `CLOUDFLARE_API_TOKEN` (a token with only the Workers AI
    permission) from the Secret `tutti-frutti-secrets` (keys `cloudflare-account-id`,
    `cloudflare-api-token`, optional); the feature is off unless both are set.
* **Privacy.** The category label, the letter, the language and the answer texts leave the
  server for Cloudflare; never nicknames, player or account ids. The privacy policy page says so
  (section 2, 4 and 5 in all three languages).
* **Metrics.** `tuttifrutti_judge_requests_total{outcome}` (`ok`, `cache_hit`, `busy`, `error`)
  and `tuttifrutti_judge_neurons_used`; both are on the gameplay row of the dashboard.

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
* Each entry: `id` (stable short key), `group`, `es`, `en`, `nl`, `default` (bool).
  Saved room state stores only ids (+ custom labels).
* **Exactly 80 entries**, **exactly 6 `default`** ("Clásicas": Nombre, Animal,
  País, Color, Comida, Marca).
* **Custom categories**: per-game only; stored in room state, copied on rematch,
  never added to the catalog. ≤ 24 chars, non-empty, no duplicates after
  normalization (also vs. built-in labels in all three languages). Total 3–10.

| Group | Categories (ES / EN / NL) |
|---|---|
| personas (9) | Nombre / Name / Naam ★ · Apellido / Last name / Achternaam · Famoso/a / Celebrity / Bekende persoon · Personaje histórico / Historical figure / Historisch figuur · Futbolista / Football player / Voetballer · Cantante o banda / Singer or band / Zanger of band · Político/a / Politician / Politicus · Escritor/a / Writer / Schrijver · Científico/a / Scientist / Wetenschapper |
| lugares (12) | País / Country / Land ★ · Ciudad / City / Stad · Capital / Capital city / Hoofdstad · Provincia o estado / Province or state / Provincie of staat · Río, lago o mar / River, lake or sea / Rivier, meer of zee · Lugar turístico / Tourist attraction / Toeristische attractie · Nacionalidad / Nationality / Nationaliteit · Club de fútbol / Football club / Voetbalclub · Monumento / Monument / Monument · Montaña o volcán / Mountain or volcano / Berg of vulkaan · Isla / Island / Eiland · Pueblo / Town or village / Dorp |
| naturaleza (7) | Animal / Animal / Dier ★ · Ave / Bird / Vogel · Animal marino / Sea creature / Zeedier · Flor o planta / Flower or plant / Bloem of plant · Insecto / Insect / Insect · Árbol / Tree / Boom · Reptil o anfibio / Reptile or amphibian / Reptiel of amfibie |
| comida (9) | Comida / Food / Eten ★ · Fruta o verdura / Fruit or vegetable / Fruit of groente · Postre / Dessert / Nagerecht · Bebida / Drink / Drank · Algo que hay en la heladera / Something in the fridge / Iets in de koelkast · Plato típico / Traditional dish / Traditioneel gerecht · Golosina / Sweet or candy / Snoep · Condimento o especia / Spice or condiment / Kruid of saus · Sabor de helado / Ice cream flavor / Ijssmaak |
| cosas (11) | Cosa / Thing / Ding · Color / Color / Kleur ★ · Ropa o accesorio / Clothing or accessory / Kleding of accessoire · Mueble / Furniture / Meubelstuk · Útil escolar / School supply / Schoolbenodigdheid · Juguete / Toy / Speelgoed · Medio de transporte / Vehicle / Vervoermiddel · Herramienta / Tool / Gereedschap · Electrodoméstico / Home appliance / Huishoudelijk apparaat · Algo que hay en la cocina / Something in the kitchen / Iets in de keuken · Algo que hay en una mochila / Something in a backpack / Iets in een rugzak |
| marcas (4) | Marca / Brand / Merk ★ · Marca de auto / Car brand / Automerk · Marca de ropa / Clothing brand / Kledingmerk · Marca de tecnología / Tech brand / Technologiemerk |
| cultura (10) | Película / Movie / Film · Serie / TV show / Tv-serie · Dibujo animado / Cartoon / Tekenfilm · Personaje de ficción / Fictional character / Fictief personage · Superhéroe o villano / Superhero or villain / Superheld of schurk · Videojuego / Video game / Videogame · Canción / Song / Liedje · Libro / Book / Boek · Dios o criatura mitológica / Mythological god or creature / Mythologische god of wezen · Disfraz / Costume / Verkleedkostuum |
| trabajo (7) | Profesión u oficio / Job / Beroep · Deporte / Sport / Sport · Instrumento musical / Musical instrument / Muziekinstrument · Parte del cuerpo / Body part / Lichaamsdeel · Materia escolar / School subject / Schoolvak · Enfermedad / Illness / Ziekte · Pasatiempo / Hobby / Hobby |
| divertidas (11) | Adjetivo / Adjective / Bijvoeglijk naamwoord · Verbo / Verb / Werkwoord · Idioma / Language / Taal · Emoción / Emotion / Emotie · Nombre de mascota / Pet name / Naam van een huisdier · Excusa para llegar tarde / Excuse for being late / Smoes om te laat te komen · Algo que llevás a la playa / Something you take to the beach / Iets wat je meeneemt naar het strand · Algo que da miedo / Something scary / Iets engs · Regalo / Gift / Cadeau · Algo que hay en una fiesta / Something at a party / Iets op een feestje · Algo rojo / Something red / Iets roods |

★ = default.

## 5. Interfaces

### REST (`/api`)

`POST /rooms` · `GET /rooms/{code}` · `GET /session` · `GET /me` ·
`PUT /me/profile` · `GET /me/stats` · `DELETE /me` ·
`GET /auth/google/login?returnTo=` · `GET /auth/google/callback` ·
`POST /auth/logout` · `GET /categories?lang=` · plus `/healthz`, `/readyz`,
`/metrics` at the root. The account endpoints and their bodies are specified in
section 9b (`web/src/lib/protocol/api.ts` is generated from `internal/api`).

### WebSocket (`/ws`)

Envelope `{ "t": string, "d": object, "v": protocolVersion }`.

* client → server: `join`, `leave`, `update_settings`, `kick`, `start_game`,
  `stop_letter`, `set_answer`, `basta`, `ask_stop`, `vote_stop`, `vote_reject`,
  `propose_merge`, `vote_merge`, `review_nav`, `ask_judge`, `finish_review`,
  `next_round`, `end_game`, `rematch`, `ping`
* server → client: `state` (filtered full snapshot), `event`, `error`.
  Engine event types: `game_started`, `round_started` (carries the chooser),
  `letter_revealed`, `basta`, `stop_asked`, `stop_rejected` (carries the asker and a
  `reason`: `declined`, `timeout` or `ended`), `time_up`, `round_locked`,
  `round_scored`, `judge_failed` (carries a `reason`: `busy` or `error`), `kicked`,
  `host_changed`, `game_over`, `rematch`; the room layer adds `server_restarting`.
* `vote_reject` takes `{category, targetPlayer, withdraw?}` (a vote can be withdrawn).
  `ask_stop` takes no payload and `vote_stop` takes `{yes}`; their error codes are
  `stop_vote_disabled`, `stop_vote_pending`, `no_stop_vote` and `stop_not_needed`, next to
  the existing `wrong_phase`, `not_participant` and `self_vote` (see "Vote to stop").
* `ask_judge` takes `{category}`; its error codes are `judge_pending`, `already_judged` and
  `nothing_to_judge` from the engine and `judge_unavailable` and `login_required` from the room
  layer (see "AI judge"). The `state` payload also carries `canJudge`. These are additive (an
  old client simply never offers the button), so the protocol version stays 3.
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
* **Vote to stop:** its electorate is evaluated live like the other votes (connected
  participants other than the asker); a player leaving, being kicked or disconnecting
  re-evaluates it, a restore drops it. Details in "Vote to stop" (section 3).
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
   (4-box code input), links: Google login, ES/EN/NL, privacy policy and
   *support the game* (side by side on one row, see "Support links").
2. **Lobby** — huge code, **Share** (Web Share API), **QR**, player list with 👑,
   settings card (host editable), category picker (search, groups, "Clásicas"
   and "Sorprendeme" presets, reorderable selection, custom category input,
   recent customs from `localStorage`), the *Ask everyone to stop* switch
   (`role="switch"`; guests see a read-only chip), **Start** (≥ 2 players).
3. **Letter** — chooser: spinning "?" + huge **STOP** button; others: "X is
   choosing the letter…"; animated reveal.
4. **Playing** — giant letter, optional timer, other players' progress dots,
   one field per category (Enter → next field, autocorrect off, capitalized),
   sticky **BASTA!/Stop!** button, Screen Wake Lock. With every field valid the
   button calls Basta (pulsing). With answers missing it is secondary and, when
   the host left *Ask everyone to stop* on, tapping it opens a confirm sheet
   (`alertdialog`: "Ask everyone to stop?", how many are missing, everybody must
   agree within 15 s; *Keep playing* is focused first). With the setting off, or
   while offline, it stays disabled as before. See "Vote to stop" (section 3).
   * **Asker:** a banner in the sticky footer ("Waiting for the others · 12 s ·
     1 of 3 said yes"), the button disabled until every field is valid (then it
     calls Basta, which ends the vote), the fields still editable; a toast
     says "Not everyone agreed" / "Time ran out" when the vote fails.
   * **Voters:** a **non-modal** sheet in the footer ("X wants to stop. Stop?",
     **Yes, stop** / **No, keep going**, a countdown). It never blocks typing or
     scrolling; it announces itself assertively and vibrates like the Basta
     call (not under reduced motion). After a yes: "Waiting for the others
     (n/total)". Spectators only see a one-line note.
5. **Basta overlay** — caller name + 3-2-1 (also when a vote to stop passed:
   the caller is the asker).
6. **Review** — category carousel ("País · 2/6"), one card per answer (player,
   answer, points badge, 👎 count, "same"), host ◀ ▶ + **Finish review**. A host
   signed in with Google also gets **Ask the AI** (≥ 48 px) for the category on
   screen; while the AI works everybody sees a status line, then every judged
   answer shows a chip ("AI: looks valid" / "AI: doubtful" plus its short reason,
   never colour alone) under a caption saying it is a suggestion that does not
   change the score. If the AI fails (budget used up or an error) the host gets a
   toast and can ask again.
7. **Round summary** — round points, totals, **Next round** (host).
8. **Final** — podium, stats, **Rematch**, **Home**, and under the round history a quiet
   *support the game* card (see "Support links").

Accessibility: touch targets ≥ 48 px, high contrast (4.5:1 for text, after opacity), `aria-live` for Basta and
letter reveal. Every screen has **exactly one `h1`** (visually hidden where the design shows none: the playing
and letter screens, "Conectando…"), a control that shows text is **named by that text** (WCAG 2.5.3: a name
that differs from the visible words, such as "60 segundos" for a button reading "60 s", is not allowed; a
longer name must start with the visible text, as the lobby code button `KT7P: tocá para copiar` and the
reject button `Rechazar 0/2: respuesta de Beto` do) and every form field has a stable `id`/`name` and
`autocomplete="off"` (`nickname` is a valid token, but Chrome reports it as wrong). The e2e suite enforces all
of this on every screen in es, en and nl (see "End-to-end tests").

The room route also has non-phase screens: an invite **gate** (`/KT7P` in a fresh tab: room check,
nickname, avatar, Join), a "Conectando…" screen, a "Reconectando…" banner over the last screen and
a **closed** screen per reason (room gone, kicked, replaced by another tab, new version, join
refused). See "Web client architecture" in 9a.

### Privacy policy page

`https://tuttifrutti.aresius.xyz/privacy` is the policy linked from the Google Cloud consent screen
(OAuth branding: *Application home page* `https://tuttifrutti.aresius.xyz`, *Application privacy policy
link* `https://tuttifrutti.aresius.xyz/privacy`, *Authorized domain* `aresius.xyz`). It is **not** an SPA
route: `web/static/privacy.html` + `privacy.css` are plain HTML with no script and no inline style (so the
strict CSP needs no change, and crawlers and people without JavaScript read it the same).

* **Languages.** One file, three `<section class="doc" id="en|es|nl" lang="…">` blocks; the URL fragment
  picks one (`/privacy#es`), pure CSS (`:target`). English is shown without a fragment or with an unknown
  one. The app's link (`PrivacyLink`, on Home and on the stats page, 48 px, `rel="external"` so SvelteKit
  does a real page load) appends the current screen language. The sticky bar has EN / ES / NL and a way
  back to the game.
* **Serving.** nginx answers `/privacy`, `/privacy/` and `/privacy.html` with `privacy.html` and
  `/privacy.css`, all `Cache-Control: no-cache` plus the usual security headers. The service worker neither
  precaches them nor treats them as an SPA fallback (`navigateFallbackDenylist`), so an installed app opens
  the real page. It needs the network; that is intentional (a corrected policy is never stale).
* **Contact.** `aresiusxp@gmail.com`, the address already used for the cluster's Let's Encrypt issuer.
* **What it says** is derived from this document and the code: the data in 9b *Privacy* (Google `sub` and
  display name only, scopes `openid profile`, no email or picture), the three cookies (`tf_guest` 1 year,
  `tf_session` 30 days, `tf_oauth` 10 minutes, all `HttpOnly`, `Secure`, `SameSite=Lax`), the browser
  storage (`tf.nickname`, `tf.avatar`, `tf.customs`, the language), retention (room snapshots until the room
  closes and never past 6 h; accounts until deleted; Loki access logs 7 days), the Google API Services
  User Data Policy *Limited Use* statement, deletion (in-app *Delete my account*, or by email, 30 days) and
  the GDPR rights, and the optional AI judge: the answers of a category (never nicknames or ids) go to
  Cloudflare Workers AI when a signed-in host asks, which acts as a processor and does not train on them.
* **Keeping it true.** Any change to what is collected, stored or set as a cookie, to a retention period or
  to the Google scopes needs the three language blocks and the *Last updated* date changed in the same
  commit. `src/lib/privacy.test.ts` checks the three languages have the same sections and facts (cookie
  names, scopes, contact, retention figures, Google links); `13-privacy.spec.ts` checks that the cookie the
  app really sets is the one the page names, that the page is reachable with an active service worker, that
  Home still fits one screen with the link, and runs axe on all three languages. `security.test.ts` fails
  if the page gains a script, an inline style or an embedded third-party resource.

### Support links

The game is free and stays free; there is a voluntary *support the game* link, nothing more (no ads, no
paywall, no perks). It is a plain outbound link (`SupportLink`, `web/src/lib/components/`) with no widget,
script or embedded resource, so the CSP and the privacy policy are untouched.

* **Where it goes** depends on the player's region, decided **in the browser** (`web/src/lib/support/support.ts`,
  a pure `supportUrl({ timeZone, languages })` plus a `detectSupportEnv()` that reads `Intl` and `navigator`
  and never throws). Argentina is a device timezone of `America/Argentina/*` (or a legacy alias:
  `America/Buenos_Aires`, `Catamarca`, `Cordoba`, `Jujuy`, `Mendoza`, `Rosario`) **or** any language tag whose
  region is `AR` (`es-AR`, `en-AR`). Those players get **Cafecito** (`https://cafecito.app/aresiusxp`, pays out in
  pesos through Mercado Pago, which is what people there use). Everybody else gets **Ko-fi**
  (`https://ko-fi.com/aresiusxp`: cards, Apple/Google Pay, PayPal). Nothing is sent to the server and there
  is **no IP geolocation** (it would need a database or service, the client address through Traefik and a
  policy change, and it is wrong for travellers and VPNs anyway).
* **The screen language does not choose the destination**, only the label: es "Invitame un café", en
  "Support the game", nl "Steun het spel". The labels never name a platform (so "Cafecito" never shows on an
  English or Dutch screen), and a visually hidden "(opens in a new tab)" follows the visible words in the
  accessible name (WCAG 2.5.3). Messages live in `web/messages/support/` (`support_*`).
* **Attributes.** `target="_blank"`, `rel="external noopener noreferrer"` (no referrer, and a real link for
  SvelteKit), at least 48 px tall.
* **Where it shows.** Home: on the same row as the privacy link (no extra height; Home still fits 360×740).
  Final: a card under the round history with one sentence, for the host and the guests, **not** in the sticky
  footer (Rematch and Home stay the main actions). Never in the lobby, during a round, in a popup or as a toast.
* **Adding a region.** Add a rule to `REGIONS` in `support.ts` (first match wins; `SUPPORT_DEFAULT` is the
  fallback), e.g. `Europe/Amsterdam` or `nl-NL` to a `bunq.me` link if that ever becomes possible, and a row to
  the table in `support.test.ts` and to `DEVICES` in `e2e/tests/14-support.spec.ts`.
* **Tests.** `support.test.ts` (every timezone / language case and the never-throws path), `SupportLink.test.ts`,
  the Home and Final component tests, and `e2e/tests/14-support.spec.ts` (see "End-to-end tests").

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
  (lint, template, kubeconform), `e2e` (Playwright against the compose stack; also
  manual).
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
* **Dependencies**: Renovate (`renovate.json`) opens the update PRs; every PR must
  carry its lockfile (`npm ci` has to pass). The build toolchain is Node 24 (CI,
  `web/Dockerfile`), Go 1.27 (`server/go.mod` is what `setup-go` reads; the
  `server/Dockerfile` image matches) and Postgres 18 (= production, bumped by hand).
  Majors that cannot work yet are disabled in `packageRules` with the blocker and
  the condition to retry in their `description`: SvelteKit 3 / adapter-static 4 /
  vite-plugin-pwa 2 (wait for `@vite-pwa/sveltekit` for Kit 3), TypeScript 7 (wait
  for `typescript-eslint` and `svelte-check`). Renovate is held on **npm 11**
  (`constraints.npm: "<12"`): npm 12 refuses to fetch registry tarballs of packages with
  bundled dependencies (`EALLOWREMOTE`, `allow-remote` defaults to none), which breaks lockfile
  maintenance because of `@tailwindcss/oxide-wasm32-wasi`. Lift the constraint when a Renovate
  lockfile-maintenance run on `web/` succeeds with npm 12. A run needs ~15 minutes and is
  started by hand with `kubectl create job --from=cronjob/renovate -n renovate`.
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
4. Accounts (Google OIDC, stats; contracts in section 9b).
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
| `internal/auth` | Signed guest cookie (`tf_guest`) → player ID; account session cookie (`tf_session`) and the Google OIDC flow (Phase 4, section 9b) |
| `internal/judge` | The AI judge of the review: Cloudflare Workers AI client with a daily Neuron budget, rate limit and cache (the only code that calls the model; see "AI judge") |
| `internal/ratelimit` | Per-IP token buckets + trusted client-IP extraction |
| `internal/ws` | WebSocket endpoint: upgrade, Origin check, read/write pumps, ping, per-connection limits |
| `internal/httpapi` | REST routes (`/api/session`, `/api/categories`, `/api/rooms`, `/api/rooms/{code}`, and the account endpoints of section 9b) |
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
   `join`** (`protocol.JoinRequest`, `v` = the protocol version: 3 since the vote to stop,
   2 since Dutch).
   The server looks the room up, applies `game.Join` and then the trusted `Game.SetConnected(now, id, true)`.
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
  Corrupt/unknown-version snapshots are logged and deleted. An open **vote to stop** is
  dropped silently first (see "Vote to stop"), and a snapshot written before the
  `stopVote` setting existed restores with it off.
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
* Metrics (Prometheus): the full list, with types, labels and what each one feeds, is the
  metrics table of "Observability (Phase 5)". Every label value comes from a fixed set in
  the code, never from client input (a test per package and the integration tests assert it).

### Persistence schema (goose, embedded, run at startup)

```sql
CREATE TABLE room_snapshots (
  code        text PRIMARY KEY,
  state       jsonb NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX room_snapshots_updated_at_idx ON room_snapshots (updated_at);
```

`users`, `user_stats` and `user_games` arrived with Phase 4 (migration
`00002_accounts.sql`, section 9b). `DATABASE_URL` empty → in-memory
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

### Accounts UI (Phase 4)

Code: `lib/account/` (store, REST client, types, `GoogleButton`, `AccountChip`, test double
`FakeAccount`) and `routes/stats/`; contracts in the REST list (§5) and 9b. Messages: `messages/account/`
(`account_*`, `stats_*`). A guest who never touches the login sees no behaviour change.

* **Account store** (`account`, runes, injectable `fetch`/timers/`navigate`/`notify`/`win`/`doc`): `status`
  (`loading` → `ready`), `googleEnabled`, `loggedIn`, `user {id, name, profile}`, `stats`, `initError`.
  `init()` runs once per page load (Home, the gate and the stats screen call it on mount): `GET /api/session`,
  and when `loggedIn` `GET /api/me` (`status` stays `loading` until both answered: no half-logged-in flash).
  `401` on `/api/me` ⇒ guest. A network/5xx failure never reaches the UI: the app stays a guest app
  (`initError` set) and `init()` is retried lazily (next `init()`, `online`, tab visible). Actions return an
  `Outcome` (`{ok}` | `{ok:false, code}`), they do not throw. `login(returnTo)` is a full-page navigation to
  `/api/auth/google/login?returnTo=…`, `returnTo` restricted to `/` or `/<ROOMCODE>` like the server (else `/`);
  `logout()` saves a pending edit first; `deleteAccount()`; both reset the state (the local nickname/avatar stay: they
  are the device's guest profile). Wire types come from the generated `lib/protocol/api.ts` (`lib/account/types.ts`
  only re-exports them under the names the feature uses).
* **Profile sync** (logged in only; nothing at all runs for a guest): the first `/api/me` of a page load decides
  once. *Saved profile present ⇒ the server wins*: nickname + avatar are adopted into the local `profile` store
  (an avatar outside the palette is ignored); *no saved profile (first login) ⇒* the local nickname is uploaded, or the Google
  display name (≤ 16 runes) becomes the nickname and is uploaded. If the user typed while `/api/me` was in flight, the
  local value wins and is uploaded (typing is never overwritten). Afterwards every nickname/avatar change is
  saved with `PUT /api/me/profile` after 800 ms of quiet (trailing debounce, at most one request in flight, never an
  empty nickname), flushed with `keepalive` on `visibilitychange → hidden` and `pagehide`. Failures are silent (the
  next change retries); after 3 consecutive failures one calm toast, again only after a success. A `401` means the
  session is gone: back to guest, no more requests. Home/gate inputs follow the adopted profile
  (`draft` re-syncs unless it already normalises to the same nickname).
* **Home:** the old "coming soon" link is one slot above the fold, in four states: reserved blank while `loading`
  (`h-12`, so nothing jumps), the secondary "Iniciar sesión con Google" button (`googleEnabled && !loggedIn`), a
  two-button row *Mis estadísticas* (→ `/stats`) | *Cerrar sesión* (`loggedIn`), or nothing (`!googleEnabled`: no dead
  link). The signed-in identity is a chip (avatar + Google name) on the left of the ES/EN/NL row, which is empty
  otherwise, so the layout is identical at 390×844 and 360×740. `?login=failed|cancelled` ⇒ a translated toast, then the
  parameter is removed with `replaceState` (from `afterNavigate`, so the router is ready; other params and the hash stay).
  The "G" is a stroked SVG path: no external image, font or Google artwork.
* **Invite gate:** a guest sees a compact "Iniciar sesión" pill in the top row (`returnTo=/<CODE>`, so the friend comes
  straight back to the same invite); a signed-in user sees the chip and the saved nickname/avatar prefilled.
* **Stats screen** (`/stats`, static SPA route, `Screen` with the Home button in the thumb zone): title, chip, five tiles
  (games, wins, total points, best round, unique answers) with derived captions computed client-side (win rate % under
  wins, average points per game under points; both hidden at 0 games, never a division by zero), a skeleton while
  loading (previous numbers stay while refreshing), an empty state at 0 games ("Todavía no jugaste ninguna partida",
  the bottom button becomes "Jugar una partida"), error + Retry, an `aria-live` status that announces each result, a
  friendly "Iniciá sesión para ver tus estadísticas" with the Google button (`returnTo=/`) for guests. *Cerrar sesión*
  goes Home with a toast. *Eliminar mi cuenta* opens a modal (`role=dialog`, `aria-modal`, the page behind it is
  `inert`, focus starts on Cancel, Tab is trapped, Escape closes, focus returns to the trigger) that says the profile and
  stats are erased for good; confirming sends `DELETE /api/me`, goes Home and toasts. A failure stays in the dialog with
  the reason; an expired session closes it.
* **Tests:** the store with a routable fake `fetch` (`account.testkit.ts`), components with `FakeAccount` through
  `vi.mock('$lib/account')`. Rune effects only run under `// @vitest-environment jsdom` (the node environment compiles
  for SSR, where `$effect` is a no-op): the store tests need it for the sync.
* **Limits / decisions:** `returnTo` cannot be `/stats` (server allow-list); the local profile is not cleared on
  sign-out or deletion; there is no account recovery UI beyond sign-in again.

### Observability (Phase 5)

Three optional chart resources, all off by default: `serviceMonitor.enabled` (scrape),
`dashboard.enabled` (Grafana dashboard ConfigMap, uid `tutti-frutti`) and
`prometheusRule.enabled` (14 alerts). The homelab enables all three in
`argocd-apps/values/tutti-frutti.yaml`. Facts about that install that the defaults
encode: Prometheus selects every ServiceMonitor (`serviceMonitorSelector: {}`) but only
rules labelled `release: kube-prometheus-stack`; the Grafana sidecar imports ConfigMaps
labelled `grafana_dashboard: "1"` from all namespaces into "General" (no folder
annotation); **Alertmanager is disabled**, so alerts are visible (Prometheus Alerts,
Grafana Alerting) but nothing notifies until a receiver is added.

**Metrics** (server `/metrics`, scraped every 30 s; `job` = server Service name,
`namespace` = release namespace). Labels are bounded: every value comes from a fixed set in
the code (unknown values collapse into `unknown` / `other`), never from client input, and the
fixed-label counters exist from the first scrape. Each metric feeds a dashboard panel or an
alert; a test (`dashboards/test_generate.py`) fails when this table, the Go sources, the
dashboard and the alerts disagree.

| Metric                                                | Type      | Labels                                                                  | Meaning                                                                                                                                    |
| ----------------------------------------------------- | --------- | ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `tuttifrutti_rooms`                                   | gauge     |                                                                         | Live rooms in memory (cap `MAX_ROOMS`, chart `server.maxRooms`)                                                                            |
| `tuttifrutti_connections`                             | gauge     |                                                                         | Clients attached to a room (joined)                                                                                                        |
| `tuttifrutti_ws_connections_open`                     | gauge     |                                                                         | All live sockets, joined or not (cap 2000 → `503 server_busy`)                                                                             |
| `tuttifrutti_commands_total`                          | counter   | `type` (19 commands, `unknown`), `result` (`ok` or the error code)      | Client frames handled by a room                                                                                                            |
| `tuttifrutti_games_started_total`                     | counter   |                                                                         | Games started by a host (event `game_started`; a rematch needs a new start and counts again)                                               |
| `tuttifrutti_games_finished_total`                    | counter   | `reason`: `completed`, `letters_exhausted`, `ended_by_host`             | Games that reached the final phase (event `game_over`), told from the state: every configured round played; no letter left; or ended early |
| `tuttifrutti_rounds_total`                            | counter   |                                                                         | Rounds started (a round that was played to the end is also in `basta_total` or `time_ups_total`)                                           |
| `tuttifrutti_basta_total`                             | counter   |                                                                         | Basta calls (a passed vote to stop counts too)                                                                                             |
| `tuttifrutti_time_ups_total`                          | counter   |                                                                         | Rounds that ended because the time limit expired (event `time_up`)                                                                         |
| `tuttifrutti_letter_auto_stops_total`                 | counter   |                                                                         | Letters the server picked because the chooser did not tap within 10 s                                                                      |
| `tuttifrutti_stop_votes_total`                        | counter   | `result`: `asked`, `passed`, `declined`, `timeout`, `ended`             | Votes to stop: opened (or passed at once), then how each ended                                                                             |
| `tuttifrutti_judge_requests_total`                    | counter   | `outcome`: `ok`, `cache_hit`, `busy`, `error`                           | Categories the host asked the AI judge about, by outcome (see "AI judge"); absent without Cloudflare credentials                           |
| `tuttifrutti_judge_neurons_used`                      | gauge     |                                                                         | Workers AI Neurons this process spent today (UTC); the Free plan allows 10000 a day, the judge stops at `JUDGE_DAILY_NEURONS` (9000)       |
| `tuttifrutti_snapshot_saves_total`                    | counter   | `result`: `ok`, `error`                                                 | Room snapshot save attempts: a failed write is retried and every attempt counts; an encoding failure is an `error`                         |
| `tuttifrutti_snapshot_save_duration_seconds`          | histogram |                                                                         | One snapshot write to the store, successful or not (buckets 1 ms … 5 s; the saver gives up after 10 s)                                     |
| `tuttifrutti_snapshot_last_success_timestamp_seconds` | gauge     |                                                                         | Unix time of the last successful snapshot write; 0 until the first one of the process                                                      |
| `tuttifrutti_snapshot_save_queue`                     | gauge     |                                                                         | Rooms whose newest snapshot waits for the store, is being written or waits for a retry (one per room, latest wins)                         |
| `tuttifrutti_snapshot_errors_total`                   | counter   |                                                                         | Failed snapshot writes, deletions and janitor sweeps (each retry counts); the writes are also in `snapshot_saves_total{result="error"}`    |
| `tuttifrutti_snapshots_deleted_total`                 | counter   | `source`: `janitor`, `room_closed`                                      | Snapshots deleted: stale rows swept by the hourly janitor (should stay ~0), or by the room that closed                                     |
| `tuttifrutti_rooms_restored_total`                    | counter   |                                                                         | Rooms rebuilt from their snapshots at startup (it only moves while the process starts)                                                     |
| `tuttifrutti_restore_duration_seconds`                | gauge     |                                                                         | How long the startup restore took (loading the snapshots and starting the rooms); 0 until it ran                                           |
| `tuttifrutti_restore_errors_total`                    | counter   |                                                                         | Saved rooms the restore could not bring back: unusable snapshots (logged and deleted) and snapshots beyond `MAX_ROOMS` (left in the store) |
| `tuttifrutti_stats_recorded_total`                    | counter   | `result`: `ok`, `error` (failed attempt, retried), `dropped` (given up) | Finished games handed to the stats store                                                                                                   |
| `tuttifrutti_stats_queue_depth`                       | gauge     |                                                                         | Finished games queued in the stats recorder (the one being written is not counted)                                                         |
| `tuttifrutti_stats_outbox_pending`                    | gauge     |                                                                         | Finished games the rooms keep in their outbox (and snapshot) until the store confirms them                                                 |
| `tuttifrutti_logins_total`                            | counter   | `result`: `ok`, `failed`, `cancelled`                                   | Google logins that reached the callback                                                                                                    |
| `tuttifrutti_ws_closes_total`                         | counter   | `code` (1000, 1001, 1006, 1008, 1009, 1011, 1012, 4001–4005, `other`)   | Why connections ended                                                                                                                      |
| `tuttifrutti_ws_send_drops_total`                     | counter   |                                                                         | Slow clients closed (1008) because their send queue was full (once per connection; the client reconnects)                                  |
| `tuttifrutti_ws_frames_total`                         | counter   | `dir` (`in`/`out`), `type`                                              | WebSocket frames                                                                                                                           |
| `tuttifrutti_http_requests_total`                     | counter   | `route` (mux pattern, `unmatched`), `method`, `code`                    | HTTP requests incl. probes and `GET /ws` (101)                                                                                             |
| `tuttifrutti_http_request_duration_seconds`           | histogram | `route`, `method`                                                       | Latency; WebSocket upgrades excluded                                                                                                       |
| `go_*`, `process_*`                                   |           |                                                                         | Go runtime and process (goroutines, heap, GC, CPU, fds)                                                                                    |

The dashboard and alerts also read kube-state-metrics (`kube_pod_status_ready`,
`kube_pod_container_status_restarts_total`, `kube_pod_container_resource_{limits,requests}`)
and cAdvisor (`container_memory_working_set_bytes`), selected by pod name (`<server>-.+`).
The web image (nginx-unprivileged) exports nothing.

How to read them:

* **A round** is started (`rounds_total`) and then ends by a Basta call, a passed vote to stop
  (both in `basta_total`) or the time limit (`time_ups_total`); so rounds played to the end =
  `basta_total + time_ups_total`, and the share ended by Basta is `basta / (basta + time_ups)`.
  A round that was abandoned (everybody left, idle room) is in none of the latter.
* **A game** is `games_started_total`, then at most one `games_finished_total`. `completed` =
  `Rounds > 0` and every round was played, whatever the host pressed on the last summary
  (the web client sends `end_game` there); `letters_exhausted` = no letter left to play;
  `ended_by_host` = the host ended it with rounds and letters left. A game that is
  abandoned or lost is in `started` only.
* **Letter auto-stop** = the engine picked the letter at the chooser's 10 s deadline (also when
  a late tap noticed it); a tap by the chooser is not counted.
* **Snapshots.** Saves are asynchronous and latest-wins per room, so `snapshot_save_queue` is
  the number of rooms with something to write (0 whenever the store keeps up).
  `snapshot_last_success_timestamp_seconds` only moves on a successful write; it is 0 in a
  fresh process until the first save, which is why the stale alert (and the "time since last
  save" panels) count from `max(last success, process start)`. A playing room saves every 5 s,
  any other live room at least every 90 minutes (`SnapshotMaxAge / 4`), so a long gap with
  nothing waiting is normal on a quiet server.
* **Restore** runs once, in `Hub.Start`, before the server listens, so the three restore
  metrics are set by the first scrape and never change afterwards (a counter that only moves
  at startup cannot drive an `increase()` alert: the panels read it directly).
* **Stats backlog.** `stats_queue_depth` is the recorder's queue, `stats_outbox_pending` the
  rooms' outboxes (a game leaves the outbox when the store confirms it, and a closed room's
  records belong to the recorder). Both are 0 when the database answers.

**Dashboard** (`helm/tutti-frutti/dashboards/tutti-frutti.json`, generated; 6 h, refresh 30 s,
variables `DS_PROMETHEUS`, `namespace`, `job`; every panel has a description saying what it
means and what to check; a purple annotation marks every server (re)start):

| Row                      | Panels                                                                                                                                                                                                                                                                                                                                                                                  |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Overview                 | Server up, uptime, rooms, connected players, open sockets (orange 1500 / red 1900), rooms created/min, games started (1 h), container restarts (24 h); rooms/players/sockets over time                                                                                                                                                                                                  |
| Gameplay                 | Games finished in range (by reason), rounds, Basta calls, time-ups, share of rounds ended by Basta, letter auto-stops, rejected-command share; rounds started vs Basta vs time-ups per min; games started and finished per hour; letter auto-stops per min; commands by type; rejected commands by result; rejected-command ratio (total and by type); votes to stop by result          |
| Connections              | Upgrades by status, close codes (readable legend), reconnect churn, 429s by route, frames in/out, policy closes (1008) next to slow-client drops (send queue full)                                                                                                                                                                                                                      |
| Persistence              | Snapshot errors (stat and rate), time since last successful save (stat and over time), rooms restored / restore duration / restore errors at the last start, snapshots deleted (stat and per hour, by source), snapshot saves by result, save error ratio, save latency p50/p95/p99, save queue, pod readiness, `/readyz` responses (503 = DB ping failing), container restarts, uptime |
| Accounts                 | Logins by result, stats recorded by result (empty until used), stats backlog (recorder queue and room outboxes)                                                                                                                                                                                                                                                                         |
| HTTP API                 | Requests by route and by status code, 5xx ratio, latency p50/p95/p99, p95 by route                                                                                                                                                                                                                                                                                                      |
| Go runtime and container | Goroutines vs expected, memory (heap, RSS, working set, limit), CPU, GC, allocation rate, file descriptors                                                                                                                                                                                                                                                                              |

Before the server exported the real counters, some panels approximated them. They now read the
exact metric (the JSON and its panel descriptions say so): *Games started (1 h)* counted accepted
`start_game` commands and now reads `games_started_total`; *Rounds ended by Basta* divided Basta
calls by rounds started (which also counts rounds still running or abandoned) and is now
`basta / (basta + time_ups)`; *Rounds started, Basta calls and time-ups* had no time-up line;
*Policy closes* could not tell a slow client from the other causes of close code 1008 and now
draws `ws_send_drops_total` next to it. New: games finished by reason, time-ups, letter
auto-stops, the snapshot save rate / error ratio / latency / queue / time since the last
successful save, restore count / duration / errors, deletions by source, the stats backlog and
the votes to stop (`stop_votes_total` had no panel).

**Alerts** (group → alert, severity, `for`; every alert carries `summary`, `description`,
`runbook` with the `kubectl` commands to run, and `runbook_url` to this section). The
server is one replica that restarts with a ~10 s gap on every deploy: during the first 4 h
in the cluster (8 pods, i.e. 7 replacements) the only scrape gaps visible at 30 s resolution
were two of 30 s, hence the long `for` on availability alerts.

| Alert                                 | Sev.     | Fires when                                                                                                                                                                                     | `for`                        | First thing to do                                                                                                                                        |
| ------------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TuttiFruttiServerDown`               | critical | `max(up) == 0` or the target is absent                                                                                                                                                         | 5m                           | `kubectl -n <ns> get pods,events`; `logs deploy/<server> --previous`                                                                                     |
| `TuttiFruttiServerRestartLoop`        | warning  | ≥ 3 container restarts in 30 min (deploys do not count)                                                                                                                                        | 5m                           | `describe pod` (Last State: OOMKilled?), previous logs                                                                                                   |
| `TuttiFruttiServerNotReady`           | critical | pod NotReady (`/readyz` fails: **Postgres ping**, or shutdown); also the "database unreachable" alert                                                                                          | 5m (startup budget is 150 s) | logs `readiness check failed`; check the shared Postgres and the `database-url` Secret key                                                               |
| `TuttiFruttiSnapshotFailures`         | warning  | any snapshot error (write, deletion or janitor sweep) in every 5 min window for 10 min (a short DB blip does not alert); the broad signal                                                      | 10m                          | logs `snapshot`; Postgres disk / connections                                                                                                             |
| `TuttiFruttiSnapshotSaveFailing`      | warning  | > 50 % of the snapshot write attempts of 10 min failed (at least 6 attempts): the saved state of the rooms is going stale                                                                      | 10m                          | dashboard Persistence row (saves by result, latency); logs `saving room snapshot failed`; Postgres disk full / read-only                                 |
| `TuttiFruttiSnapshotStale`            | warning  | rooms exist and nothing was saved for 15 min while snapshots are waiting (`snapshot_save_queue > 0`), or for 2 h whatever the queue; counted from the later of the last save and the pod start | 5m                           | dashboard Persistence row (time since last save, save queue); logs `snapshot`; Postgres; nothing waiting for 2 h = stuck refresh timers: restart the pod |
| `TuttiFruttiRateLimitSaturation`      | warning  | > 6 requests/min answered 429 (global buckets: real players refused)                                                                                                                           | 5m                           | dashboard Connections row; one abusive client?                                                                                                           |
| `TuttiFruttiWebSocketUpgradesFailing` | warning  | > 50 % of upgrades (≥ 3/min) not 101                                                                                                                                                           | 10m                          | status code on the dashboard (401 cookie, 403 Origin → `server.publicUrl`/`allowedOrigins`, 429, 503)                                                    |
| `TuttiFruttiHighErrorRate`            | warning  | > 5 % 5xx of user traffic (probes excluded, ≥ 1.2 req/min)                                                                                                                                     | 10m                          | logs `"status":5`                                                                                                                                        |
| `TuttiFruttiGoroutineLeak`            | warning  | `go_goroutines` > 100 + 4 × open sockets + 3 × rooms (healthy: ~2 per socket and room + ~15)                                                                                                   | 30m                          | goroutine panel; restart and open an issue                                                                                                               |
| `TuttiFruttiMemoryHigh`               | warning  | working set > 85 % of the container limit                                                                                                                                                      | 15m                          | memory panel; raise the limit or lower `server.maxRooms`                                                                                                 |
| `TuttiFruttiMemoryGrowth`             | warning  | linear trend of 6 h would exceed the limit within 4 h (pods older than 6 h only)                                                                                                               | 30m                          | memory panel vs rooms; restart at a quiet hour                                                                                                           |
| `TuttiFruttiStatsRecordingFailing`    | warning  | stats write errors in every 5 min window for 10 min                                                                                                                                            | 10m                          | logs `game statistics`; Postgres                                                                                                                         |
| `TuttiFruttiStatsDropped`             | warning  | any finished game given up in 10 min (stats of that game lost)                                                                                                                                 | 0m                           | same; fix the database                                                                                                                                   |

**Runbook: the three snapshot alerts.** They watch different things and usually fire together
when Postgres misbehaves, with `TuttiFruttiServerNotReady` (critical) when it is unreachable:

* `SnapshotFailures` is the sensitive one: any failed write, deletion or janitor sweep, every
  5 minutes for 10. A deletion that keeps failing keeps the room code reserved and the row in
  the store.
* `SnapshotSaveFailing` says that the *write path* is mostly failing (more than half of the
  attempts, so a few failures among many good writes never fire it).
* `SnapshotStale` says that *nothing at all* got through: it needs rooms to exist, and either
  snapshots waiting for 15 minutes (the saver retries forever with a 10 s backoff cap) or
  2 hours without any save (idle rooms refresh every 90 minutes, so this means the refresh itself
  is stuck). It counts from the later of the last save and the start of the pod, because the
  timestamp is 0 until the first save: a deploy does not fire it, and a store that is down since
  the start does, 15 minutes after it (plus the `for`).

In all three: `kubectl -n <ns> logs deploy/<server> --tail=200 | grep -i snapshot`, the dashboard
Persistence row (saves by result, latency, queue, time since last save), then the shared Postgres
(disk full, read-only, connection limit, restart) and the `database-url` Secret key. Rooms keep
running from memory meanwhile: the risk is a restart before the store is back, which restores old
rooms or none. After the fix the queue drains by itself (the savers retry) and the alerts
resolve; there is nothing to replay.

**Extending.** Dashboard: edit `dashboards/generate.py` (helpers `stat`, `timeseries`,
`state_timeline`; the layout engine places panels left to right on the 24-column grid),
run it, commit the JSON. `dashboards/test_generate.py` (part of the CI check) fails when a
panel or an alert reads a `tuttifrutti_*` series the Go code does not register, when a
registered metric feeds neither a panel nor an alert, and when the metrics and alerts tables
above drift from the code: a new metric needs its table row and a panel or alert in the same
change. Before committing, run every new expression against a live Prometheus
(`/api/v1/query_range`) and make sure it returns data or an intentionally
empty result; use `$__rate_interval`, set units and thresholds, and guard divisions with
`/ (x > 0)`. The JSON is written with a one-space indent on purpose: `kubectl apply` stores the
ConfigMap twice (the object and the last-applied annotation, capped at 256 KiB) and the test
keeps it well under that. Alerts: add the rule to `templates/prometheusrule.yaml` (Helm-escape
Prometheus templates as ``{{`{{ $value }}`}}``), add a firing and a not-firing case to
`tests/alerts_test.yaml` (the snapshot alerts also test a deploy restart, where the timestamp
gauge of the new pod starts at 0), and run `helm/tutti-frutti/ci/check-observability.sh`
(`PROMTOOL=<path>`; without a local promtool: a wrapper around
`docker run --entrypoint promtool prom/prometheus:v<version>`). The script is the CI check
(`.github/workflows/ci-helm.yml` installs promtool and runs it, in addition to
`helm lint --strict` and `kubeconform -strict -ignore-missing-schemas -kubernetes-version 1.30.0`
with `--set serviceMonitor.enabled=true --set dashboard.enabled=true --set prometheusRule.enabled=true`).

### Security headers and PWA (Phase 5)

**Where.** Every response of the web image carries the headers: `web/nginx-security-headers.conf`
(`add_header … always`) is included at server level and again in every location that sets its own
header (nginx does not inherit `add_header` otherwise), so `/healthz`, the JSON 404 of the backend
namespace and asset 404s have them too. `web/nginx.conf` holds the per-request `map`s, and a test
(`src/lib/security.test.ts`) fails when a location sets a header without the include. Responses of
the Go server (`/api`, `/ws`) get their headers from the server, not from this image.

**Content-Security-Policy** (one line, no `unsafe-inline`/`unsafe-eval` anywhere):

```
default-src 'self'; script-src 'self' 'sha256-<bootstrap>'; style-src 'self';
style-src-attr 'unsafe-hashes' 'sha256-<wrapper>' 'sha256-<announcer>'; img-src 'self'; font-src 'self';
connect-src 'self' ws://<host> wss://<host>; manifest-src 'self'; worker-src 'self'; object-src 'none';
base-uri 'self'; form-action 'self'; frame-ancestors 'none'  [; upgrade-insecure-requests  (https only)]
```

- `script-src`: the SPA shell has ONE inline script (SvelteKit's bootstrap, its text embeds content-hashed
  chunk names, so its hash changes on every build). Everything else is external (`'self'`); the service worker
  and `workbox-*.js` are same-origin files.
- `style-src 'self'`: Tailwind ships one external CSS file and Svelte's `style:` directive / `el.style.x = …`
  go through the CSSOM, which CSP does not restrict (verified: `style.cssText =` and `style.setProperty` raise no
  violation). Static `style="…"` attributes in a template ARE restricted (`style-src-attr`). Two exist and are
  allowed by hash (`'unsafe-hashes'`, no `'unsafe-inline'`): the shell's `<div style="display: contents">` and
  SvelteKit's route announcer (`#svelte-announcer`, rendered at start; without its style the screen-reader-only
  text would become visible). Chromium found the announcer: it is exactly the kind of thing a real browser run
  catches and reading the code does not.
- `img-src 'self'`: the QR is an inline `<svg>`, avatars are emoji text, the CSS has no `url()`: no `data:`.
- `connect-src`: `'self'` plus `ws://` and `wss://` of the request's own host (`$http_host` through a validated
  `map`: only `[a-z0-9.-]` + optional port is ever interpolated, anything else falls back to `'self'`). Safari has
  historically not matched `ws(s):` with `'self'`; naming the host costs nothing and works in every deployment
  without configuration (Caddy forwards the original `Host`, verified; Traefik does by default, `passHostHeader`). `ws://` on an https page is blocked
  by mixed-content rules anyway, so listing both schemes weakens nothing and cannot fail closed when a proxy omits
  `X-Forwarded-Proto`.
- `form-action 'self'`: the Google sign-in is a top-level navigation (`<a href="/api/auth/google/login">`) that
  302s to accounts.google.com: navigations are not restricted by these directives. Verified in Chromium with a
  link, `location.href` and a `<form method=get>` whose target 302s to another origin and back: all work.
- `frame-ancestors 'none'` (with `X-Frame-Options: DENY`), `object-src 'none'`, `base-uri 'self'`.
- `upgrade-insecure-requests` only when `X-Forwarded-Proto` is `https` (cluster); it would break plain-http
  localhost/compose, so those requests do not get it.
- Not enabled on purpose: `report-uri`/`report-to` (no collector), Trusted Types (Svelte builds DOM from
  `innerHTML` templates), COEP (not needed, would break cross-origin embeds we do not have).

**Hash flow (header and meta can never disagree).** There is no `<meta http-equiv>` CSP: the nginx header is the
only policy (the build fails if `index.html` carries one). `svelte.config.js` wraps `adapter-static`; right after it
writes `build/`, `scripts/csp.mjs` reads `build/index.html` and the client bundle and writes
`.svelte-kit/nginx/csp-hashes.conf` (two `map`s: `$tf_script_hashes`, `$tf_style_attr_hashes`), which the
Dockerfile copies next to the snippet (`RUN nginx -t` then validates the whole config at image build). The script
**fails the build** (so `npm run build`, CI and the Docker build fail) when the shell contains anything the policy
would block: a second inline `<script>`, an inline event handler, a `javascript:` URL, an inline `<style>`, a CSP
`<meta>`, or a static `style="…"` attribute (in `index.html` or in a template of the client bundle) that is not in
`EXPECTED_STYLE_ATTRS`. Fix: move the code to a file, use `style:prop={…}`, or extend the policy deliberately. The
Vitest tests cover the generator (known hash vector, every rejection), the policy text (required directives, no
wildcard/unsafe sources), the nginx structure and, when `build/` exists, the real output.

**Other headers.** `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
`X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`
(verified harmless: the Google round trip, the PWA, and the app still boots after it; cross-origin framing and
embedding of our files are refused), `X-Permitted-Cross-Domain-Policies: none`, and
`Permissions-Policy` denying accelerometer, camera, display-capture, geolocation, gyroscope, hid, magnetometer,
microphone, midi, payment, serial and usb. Wake Lock, Web Share, clipboard writes and vibration are left at their
default (this origin); only names Chromium recognises are listed, unknown ones log a console warning.
`Strict-Transport-Security: max-age=31536000` (no `includeSubDomains`, no `preload`) is sent only when
`X-Forwarded-Proto` is `https`, so it never appears on the http compose stack. Cache headers: unchanged, except
that `Cache-Control` on `/_app/immutable/*` and on images no longer uses `always`: before, a **404** there (e.g. a
chunk of the other version during a rolling deploy) was cached as `immutable` for a year.

**Debugging a violation.** The console says `Refused to … because it violates the following Content Security
Policy directive: …` and names the directive; `document.addEventListener('securitypolicyviolation', …)` gives
`violatedDirective`, `blockedURI` and, for inline code, the hash the browser wants. Check the served policy with
`curl -sI http://localhost:8000/ | grep -i content-security`. Playwright: `page.exposeFunction` + that listener, plus
CDP `Audits.enable` (`ContentSecurityPolicyIssue`). The policy blocks (verified): inline scripts and handlers,
`eval`/`new Function`/`setTimeout(string)`, `javascript:` URLs, foreign `fetch`/WebSocket/`<img>`/`<script>`/
stylesheet/`<iframe>`/`<object>`/`<base>`/form posts, `data:` images, `setAttribute('style')`, and framing.

**PWA checklist (all verified over `http://localhost` in Chromium: `Page.getInstallabilityErrors` is empty, no
manifest warnings).** Manifest (`web/scripts/pwa-manifest.ts`, built from `BRAND`): `id`, `name`/`short_name`,
`description`, `start_url`, `scope`, `display: standalone`, `orientation: portrait`, `lang`, `dir`, `categories:
[games]`, `theme_color` = `background_color` = `--color-night` = `<meta name="theme-color">` (a test keeps the four in
sync), icons 192/512 `any` + 512 `maskable`, `apple-touch-icon` (180), iOS meta tags. The first paint never flashes:
the stylesheet is render-blocking, so the first frame already has the night background (a screencast with the CSS
delayed 2 s shows no frame before it), and the splash uses `background_color`. `screenshots`: four narrow 540×960
PNGs (26–41 KB, `static/screenshots/`, from the dev playground with `node scripts/screenshots.mjs`), which Chrome
needs for its richer install UI; they are excluded from the precache (`workbox.globIgnores`) so no client downloads
them unless the install dialog asks. No `wide` screenshots (mobile-first game). No `shortcuts`: "join a game" would need
app support (`/?join=1`), not trivial.

**Link previews (Open Graph).** Chat apps (Discord, WhatsApp, Telegram, Slack, …) unfurl a pasted URL with a crawler
that runs **no JavaScript**, so everything it needs is in the static shell `web/src/app.html` (the runtime
`<svelte:head>` of the layout is invisible to it): `og:title` (= `BRAND`), `og:site_name`, `og:type`,
`og:description`, `og:locale` (`es_AR`), `og:image` (absolute `https://tuttifrutti.aresius.xyz/og-image.png`, as
crawlers need) with `og:image:type/width/height/alt`, and `twitter:card = summary_large_image` (which Discord uses to
show the large card). The preview is Spanish only (the base language; the crawler sends no usable language and the
shell is one static file). There is deliberately no `og:url`: invite links (`/KT7P`) are served the same shell and
must not all name the home page as their canonical URL. The image is 1200×630 (~35 KB palette PNG), with everything
that matters inside the central square (WhatsApp/Telegram may crop to 1:1) and no small text; it is rendered by
`npm run og` (`web/scripts/og-image.mjs`: the logo mark and the wordmark colours of the app, in Chromium) and
committed. It is excluded from the precache (`workbox.globIgnores`) because the app never needs it (only chat crawlers fetch it), and nginx serves
it with the generic image rule (`Cache-Control: public, max-age=86400`). Chat apps cache the card per URL, so after a
change post the link with a throwaway query string (`/?v=2`) to see it. `src/lib/social.test.ts` checks the tags and
the file, and the e2e spec in `08-pwa.spec.ts` fetches `/` and `/KT7P` without JS and the image over HTTP.

**Service worker and update flow** (Workbox `generateSW`, `registerType: 'prompt'`). It precaches the shell, navigations
fall back to `index.html` except `/api`, `/ws`, `/healthz`, `/readyz`, `/metrics` (denylist + `NetworkOnly`). A new
version installs in the background and WAITS; `UpdatePrompt` shows "Nueva versión disponible → Recargar" and only the
click sends `SKIP_WAITING` and reloads. There is no `clientsClaim` and no automatic `skipWaiting`
(a test checks the built `sw.js`), so a game is never reloaded behind the player's back. `cleanupOutdatedCaches`
and Workbox's precache cleanup remove the old revisions on activation. Verified with two builds (v1 → v2 served on
the same port, page left open): the prompt appeared, the new worker stayed `waiting`, the page was not reloaded or
navigated for 6 s, the click loaded v2, one precache cache remained without v1-only chunks. Offline (origin
unreachable + `navigator.onLine=false`): `/`, `/KT7P` and `/stats` load from the precache, the offline banner shows and
goes away when the browser is back online, `fetch('/api/session')` and a navigation to `/api/auth/google/login` fail
as network errors instead of being answered by the worker.

**Full-game verification.** `docker compose -p tf-c` with the released server `0.3.0` and the web image built from
this tree: a 3-player game in Chromium (Home, language switch, invite gate, lobby with copy-fallback, QR sheet and
category picker, letter, playing with Wake Lock, offline/online, review, summary, final, rematch, leave) in Spanish
and English, over plain http and over TLS (`https://` with `Strict-Transport-Security`, `upgrade-insecure-requests`
and `wss://`): zero `securitypolicyviolation` events and no Permissions-Policy warnings.

### End-to-end tests (Phase 5)

`e2e/` (Playwright + TypeScript, own `package.json`; see `e2e/README.md`) drives the real compose stack
(Postgres, Go server, nginx web, Caddy edge) through Chromium with Pixel 7 emulation (390×844). A player is a browser
context (own cookies and storage); a test is a conversation between players. Base URL `E2E_BASE_URL` (default
`http://localhost:8000`); `E2E_COMPOSE_PROJECT` names the stack for the server-restart test (skipped without it).

**Projects.** `chromium` (every spec but the next two, ~2 min), `slow` (`*.slow.spec.ts`: the 60 s round time limit and the 15 s
vote-to-stop timeout, which must run against the server's own clock; the shortest allowed limit is 60 s) and `restart`
(`*.restart.spec.ts`: restarts the server container, so it runs alone after the other two via project dependencies).
Two workers, one retry in CI, trace on failure, no video, HTML + list reporters.

**Specs.** (1/2/10) the reference 3-player game in Spanish, English and Dutch: create, join by code and by lowercase invite link,
settings visible to guests, chooser order, Basta overlay (`¡Basta para mí, basta para todos!` / exactly `Stop!`),
review with two rejections, totals equal to a hand-computed table, finish-early confirmation, last-round summary,
podium, rematch, leave, second game from zero; in English and Dutch no Spanish character or word on any screen.
The Dutch game also proves the `ĳ`/`ij` rule end to end (`Ĳsland` = `IJsland`, answers shown as typed, one shared answer
worth 5 each; `Ysland` is not an I) in a round whose letter is aimed at I by timing the chooser's tap.
(3) custom categories (2 built-in + 1 custom, duplicates by case/accents/built-in names refused). (4) time limit.
(5) hard letters and language: letter list, rounds limit (26/23 in Dutch), several rounds never reveal a hard letter. (6) reload, network
loss and server restart mid-round keep room, round, seats and answers. (7) unknown room, full lobby of 10 (WebSocket
bots), kicked player, mid-round joiner, second tab "replaced", finished game. (8) service worker, offline shell,
manifest. (9) axe on every screen of the game in all three languages (Home, the invite gate and its "no such room" state,
the lobby as host and as guest with a time limit chosen, its QR / confirm-removal / leave sheets, the category picker and
its custom field, the letter screen as chooser, waiting and late joiner, playing with the clock running, as a watcher and
with an empty and a wrong-letter field, the review before and after a "same" proposal and a rejected answer, as host,
guest and spectator, the summary, the final, the vote to stop, the closed screens, the offline banner and the account
screens): **no `serious`/`critical` violation, and none of the two moderate rules `page-has-heading-one` (at least one `h1`)
and `label-content-name-mismatch` (a control's name contains the text it shows, WCAG 2.5.3), and exactly one `h1` in the
accessibility tree** (axe alone accepts several; the check counts after axe, so a screen without any `h1` is still named
by the axe rule); every other moderate finding is only reported. The allow-list `KNOWN_VIOLATIONS` in `e2e/src/a11y.ts` is **empty**: the mechanism
stays (per element, with a reason), and a safety-net test proves that a violation of either kind fails, that an
allowance silences exactly what it names, that the two moderate rules block and that a second `h1` fails (a `display: none`
one does not count). Not reachable in a browser run: the
0.9 s Basta overlay and the reconnecting pill (covered, with every other playground state, by the jsdom sweep
`web/src/lib/a11y.test.ts`). (11) first-visit language from the
browser (`nl-NL`/`nl-BE` → Dutch, `en-GB` → English, `es-ES`/`fr-FR` → Spanish); a choice in the switch beats the
browser and survives a reload. (12) the vote to stop: with a category empty the button stays enabled and opens the
confirmation (`alertdialog`, focus on "keep playing", Escape sends nothing); the asker's banner and the voters'
non-modal prompt (typing is not blocked), "n of total", the last yes shows the Basta overlay with the asker's name and
leads to lock, review and scores with the empty cell at 0 (the asker sent no `basta`); a no rejects at once (only the
asker is told, nobody is named) and she can ask again right away; with the lobby switch off the button is disabled
until everything is valid; another player's Basta ends the vote and the overlay names that player; a voter going
offline or closing the tab leaves the electorate, the asker going offline cancels the vote; a spectator sees the
onlooker line and cannot vote (a bot spectator's `vote_stop` gets `not_participant`); the whole vote also in English and
Dutch (exact words, `Stop!`, no Spanish on any screen); axe on the confirmation and the voter's prompt in all three
languages; at 360×740 in Dutch with a 16-letter name the focused field stays wholly visible while the prompt is open;
the 15 s timeout runs for real in the `slow` project (`12-stop-vote-timeout.slow`, es/en/nl). The older specs adapted
to the new contract: where a field is missing the button now offers the vote instead of being disabled (1/2/10, 4).
(13) the privacy policy: Home shows the 48 px link in all three languages without scrolling, it opens
`/privacy#<language>` as a real page load (no script, no app shell, no sideways scroll) with the right title,
sentence and `lang`, the way back leads to the game; without a fragment (or with an unknown one) the page is English,
the EN/ES/NL bar switches language and stays on screen, `/privacy/`, `/privacy.html` and the stylesheet answer, the
page is `no-cache` with the app's security headers, an app whose service worker is active still shows the policy, the
cookie a guest really gets (`tf_guest`, `HttpOnly`, `Lax`, about a year) is the one the page names, every outgoing link
goes to one of the five known hosts, and axe is clean in es, en and nl.
(14) the support link: on Home (360×740, in the same row as the privacy link, no scrolling, no extra request to any
other host) and on the Final screen (host and guest, outside the footer, axe clean) the link goes to Cafecito for
`America/Argentina/Buenos_Aires` / `Cordoba` / the legacy `America/Buenos_Aires` and to Ko-fi for Amsterdam, Madrid
and Montevideo whatever the screen language; it is `target=_blank` with `noopener noreferrer`, at least 48 px tall, wholly
on screen, with no Spanish on the English and Dutch screens; switching the language changes the label and never the
destination. The links are never opened.

**Rules of the suite.** (A spec of its own checks that the guard, the no-Spanish check (English and Dutch) and the accessibility check really fail when they should; the main assertions were also mutation-checked against deliberately broken servers and messages.) Labels come from the app's real `web/messages/**` (never retyped, so they cannot drift); the
exact Basta words and the game rules (alphabets, hard letters, points) are written out in the tests, so drift from this
document fails a test. Selectors are roles and labels. No fixed sleeps: auto-waiting assertions and `expect.poll`
(short-lived UI such as the 0.9 s Basta overlay is recorded with a `MutationObserver`, not raced). A global guard
fails any test on an uncaught page error, a console `error`, a failed or `>= 400` same-origin request, and on any
message that mentions a Content Security Policy violation; expected noise (a provoked 404, the offline window, the
server restart) is allowed per test with a stated reason. `context.setOffline` leaves an open WebSocket alone, so a
"lost connection" also closes the page's room socket.

**Rate limits.** The per-IP buckets act as one global bucket in compose and in the cluster (room creation 30/min,
burst 10; lookups and WebSocket upgrades 300/min, burst 60 each) and `TRUST_FORWARDED_FOR` is off in compose, so
the suite stays under them by construction: each worker mirrors its share of the creation bucket and waits before
overdrawing it (about 25 rooms per run).

**CI** (`.github/workflows/e2e.yml`): `workflow_dispatch` and pull requests touching `server/**`, `web/**`, `e2e/**`,
`docker-compose.yml`, `deploy/**` or the workflow; lint of the suite, npm and Playwright-browser caches, server and
web images built with the same `gha` layer-cache scopes as `docker-build.yml`, `docker compose up --no-build --wait`,
`E2E_COMPOSE_PROJECT=tutti-frutti`, compose logs on failure, report and traces uploaded on failure, 30 minute limit,
newer runs of the same ref cancel older ones.

**Not covered on purpose:** real Google login (needs credentials and a consent screen), other browsers and desktop
sizes, load, Kubernetes/Traefik specifics.

## 9b. Phase 4 — accounts and stats (contracts)

Playing never needs an account. A Google login adds two things for a returning
player: a saved nickname/avatar that prefills the join form, and lifetime stats.
The web client is described in "Accounts UI (Phase 4)"; the response bodies live
in `server/internal/api` (→ generated `web/src/lib/protocol/api.ts`).

### Cookies

| Cookie | Value | Attributes |
|---|---|---|
| `tf_guest` | `<playerId>.<mac>` (section 9a) | `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` on https, 1 year |
| `tf_session` | `<userId>.<expiryUnix>.<mac>`, `mac = base64url(HMAC-SHA256(HMAC(SESSION_SECRET, "tf-session-v1"), userId + "." + expiryUnix))` | same, `Max-Age` 30 days |
| `tf_oauth` | `<base64url(json{state,verifier,nonce,returnTo,expiry})>.<mac>`, key `HMAC(SESSION_SECRET, "tf-oauth-v1")` | same, `Path=/api/auth/google`, 10 min; expired by every callback |

* The three keys are derived from the one secret with different labels (the guest
  cookie uses the secret itself), so a MAC for one cookie never verifies for
  another, even if a client renames a cookie. MACs are compared in constant time
  with a strict base64 decoder; a value over 91 bytes (`tf_oauth`: 1 KiB), with a
  non-canonical expiry (leading zero, sign, hex), a non-UUID user id, an expiry in
  the past or more than 30 days + 1 min ahead is refused before any hashing or
  after one MAC check.
* The player id **stays the guest id**: logging in does not change it. The
  WebSocket still authenticates with `tf_guest`; a valid `tf_session` on the
  upgrade request only links the player to the user (an invalid or missing one
  is a guest, never an error). A session without a guest cookie is refused like
  any request without one.
* Sessions are stateless: logout clears the browser's cookie but cannot revoke a
  copy. Deleting the account does (every account endpoint checks that the user
  still exists). There is no sliding renewal: a login gives 30 days.
* `Secure` is set when `PUBLIC_URL` is https. Cookies are never logged.

### Endpoints

All JSON, all `Cache-Control: no-store` (a small middleware also sets
`X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin` on every
response and defaults `no-store` for everything under `/api/`; the categories
handler overrides it). Errors are `{"error":"<code>"}`. Unsafe methods go through
the Origin check (`403 forbidden_origin`). Bodies ≤ 4 KiB, `application/json`
(else `415 unsupported_media_type`, `413 payload_too_large`, `400 bad_request`
for malformed JSON or unknown fields).

| Endpoint | Answer |
|---|---|
| `GET /api/session` | `{"guest":true,"googleEnabled":bool,"loggedIn":bool}`; mints `tf_guest`; `loggedIn` = a valid unexpired `tf_session`, **no database lookup** |
| `GET /api/me` | `{"id","name","profile":null\|{"nickname","avatar":{"emoji","color"}}}`; `name` = Google display name (cleaned, ≤ 16 runes, may be empty); `401 unauthorized` without a valid session **or** when the user no longer exists (the cookie is then expired) |
| `PUT /api/me/profile` | body `{"nickname","avatar":{"emoji","color"}}` → `204`; nickname through `game.CleanName` (`400 invalid_name`), emoji and color non-empty, ≤ 32 bytes, no control characters (`400 invalid_avatar`); `401` |
| `GET /api/me/stats` | `{"gamesPlayed","wins","totalPoints","bestRound","uniqueAnswers"}` (integers, all 0 before the first game); `401` |
| `DELETE /api/me` | `204`: deletes the user, profile, stats and game rows (cascade) and expires `tf_session`; the guest cookie stays; `401` |
| `POST /api/auth/logout` | `204`, expires `tf_session` (also without a session) |
| `GET /api/auth/google/login?returnTo=` | `302` to Google; `404 auth_disabled` when Google is not configured; `429 rate_limited` (+ `Retry-After`); if Google's discovery fails, `302 /?login=failed` (the browser is mid-navigation: never leave it on JSON) |
| `GET /api/auth/google/callback?code&state` | **always `302`, empty body**, `Location` is a constant of ours: success → `returnTo` with `tf_session`; any failure → `/?login=failed`; `error=access_denied` → `/?login=cancelled`; rate limited → `/?login=failed`; `404 auth_disabled` when Google is off. Query parameters are never echoed, never logged and never decide where it redirects |

Every other `401` clears a stale `tf_session` the browser still sends.

### Google OIDC (authorization code + PKCE)

* Libraries: `golang.org/x/oauth2`, `github.com/coreos/go-oidc/v3`. `redirect_uri`
  = `PUBLIC_URL` + `/api/auth/google/callback` (register exactly this URI; no
  JavaScript origins are needed). Scope **`openid profile`** only (no email),
  `prompt=select_account`, no offline access; the access token is discarded.
* `login` builds the request: random `state`, `nonce`, PKCE verifier (S256
  challenge), stores them with the sanitised `returnTo` in `tf_oauth`, redirects.
  **`returnTo` allow-list:** exactly `/` or `/<ROOMCODE>` (4 characters of
  `BCDFGHJKLMNPQRSTVWXZ23456789`, any case, normalised to upper case); anything
  else, however clever (`//host`, `/\host`, a scheme, dot segments, encoded or
  doubled slashes, CRLF, long strings), becomes `/`. No trimming, no decoding.
* `callback` checks, in this order: the `tf_oauth` signature and expiry (a
  missing cookie fails, so a replayed or forged callback never reaches Google);
  `state` equal to the cookie's (constant time); `error` (`access_denied` →
  cancelled); `code` present (≤ 2048 bytes); the code exchange (client secret in
  the POST body, PKCE verifier; the code is single use at Google); an `id_token`
  in the response; its signature against Google's JWKS (**RS256 only**: `none`,
  HS256 and other algorithms are refused), `iss = https://accounts.google.com`,
  `aud` contains the client id (and `azp` equals it when there are several),
  `exp` in the future, `nonce` equal to the cookie's (constant time), `sub`
  non-empty, ≤ 255 bytes, no control characters. Then `name` is cleaned (control
  and invisible format characters dropped, whitespace collapsed, 16 runes),
  the user is created or refreshed (`last_login_at`, name; the saved profile is
  kept) and `tf_session` is issued.
* **Lazy discovery:** nothing touches the network at startup. The first login
  fetches `.well-known/openid-configuration` (the JWKS follows on the first
  verification, refetched by go-oidc on an unknown `kid`); a failure is retried
  by the next login, a success is cached. All requests to Google share an HTTP
  client with a 10 s timeout, response bodies over 1 MiB refused and redirects
  never followed (the token request carries the client secret).
* Logs name the stage that failed (`state`, `exchange`, `id_token`, `nonce`...),
  never a token, code, state, verifier, nonce, secret or cookie.
* The issuer and its transport are constructor parameters (tests run against a
  fake provider with its own RSA key: `internal/auth/authtest`); there is no
  environment variable for them.

### Stats

A game goes through three stages in its room (all on the room's goroutine; only
the delivery is in the background):

1. **Game start.** At `game_started` the room mints the **game id** (a random
   UUID, new for every game and every rematch, cleared by the rematch). It is
   part of *every* snapshot of a game in progress, so a game replayed after a
   crash (restored from a snapshot taken before `game_over`) ends under the same
   id. A restored started game without an id (an older snapshot) gets one.
2. **Game over.** The room builds the `GameRecord` once (game id, `PlayedAt`, one
   result per credited user), appends it to its **outbox** (`pending`, at most 8
   records, part of every snapshot, schema field `pending` + marker `outbox: 1`)
   and offers it to the recorder. The record is frozen: it is never rebuilt from
   live state, so a later reconnect, leave or rematch cannot change who gets
   credit. (A final-phase snapshot of a server without the outbox, which has a game
   id and no marker, rebuilds its record once at restore.)
3. **Delivery.** When the recorder reports that the store has the record (or
   refused it for good) the room removes it from the outbox and saves. A record
   the recorder gave up on (attempts exhausted, queue full, shutdown) **stays in
   the outbox**: it is offered again at the room's next snapshot, by the next
   process at its start, and, if the room is closed for good, handed to the
   recorder one last time. Beyond 8 the oldest is given up (`dropped`).

Credit rules:

* Players linked to a user who took part in **≥ 1 scored round**, in a game with
  **≥ 2 participants** (players that appear in a scored round). A player who left
  still gets the rounds they played but cannot win (only `Winners` win; a tie is
  a win for everybody who tied). Two seats linked to the same user (two devices)
  count once, the first seat.
* **The link player → user** is taken at `join` from the connection's
  `tf_session`, kept in the room's snapshot and dropped with the seat. It follows
  the latest join **only until the player takes part in the game** (they are a
  participant of the round in progress, or appear in a scored round); from then
  until the rematch it is **frozen**, a missing link included. So a guest who
  signs in after the game started gets no credit for that game (sign in before
  starting), a signed-out reconnect or an expired session does not cost the user
  their credit, and nobody can move the credit of rounds already played to another
  account by reconnecting. At the rematch the history is gone and the next connect
  refreshes the links.
* Per user: `gamesPlayed += 1`, `wins += 1` if in `View.Winners`, `totalPoints +=
  final score`, `bestRound = max(bestRound, best single-round total)`,
  `uniqueAnswers += number of 10-point cells`.

Guarantees:

* **Idempotent per (user, game id)**: `RecordGame` inserts the `user_games` row
  `ON CONFLICT DO NOTHING` and updates `user_stats` only when the row was
  inserted, in one transaction; users that no longer exist are skipped silently
  (a share lock on the user's key keeps a concurrent `DELETE /api/me` safe).
  Together with the id minted at the start, retries, restarts, a crash between
  the commit and the next snapshot, and a double delivery never double count.
* **Delivery never blocks a room.** The recorder is one goroutine per hub with a
  bounded queue (256 games; a game that does not fit stays in its room's outbox):
  failures are retried with exponential backoff (1 s → 30 s, on the hub's clock,
  at most 20 attempts per offer); a game the store refuses as invalid or a panic
  in the store do not stop the queue. At shutdown (after the rooms stopped
  producing and within the existing 15 s hub budget) every queued game gets one
  last attempt; the final snapshots already carry the outbox, so what still fails
  is recorded by the next process.
* **What can still be lost:** a game whose room is closed for good (idle, everyone
  left) while the store has been down for longer than the process lives; a process
  killed before the snapshot with the outbox is durable (the game is then replayed
  or never reached `game_over`); an outbox that overflows (8 unrecorded games in
  one room).
* Metrics (constant cardinality): `tuttifrutti_logins_total{result=ok|failed|cancelled}`
  (callbacks that ended in each outcome), `tuttifrutti_stats_recorded_total{result=ok|error|dropped}`:
  `ok` = a game recorded, `error` = a failed attempt (it is retried), `dropped` =
  a game that will never be recorded (refused by the store, evicted from a full
  outbox, unreadable in a snapshot, or abandoned with its room).

### Schema (`00002_accounts.sql`)

```sql
users      (id uuid PK, google_sub text UNIQUE NOT NULL, name text NOT NULL,
            nickname, avatar_emoji, avatar_color text NULL (all or none, CHECK),
            created_at, last_login_at timestamptz)
user_stats (user_id uuid PK → users ON DELETE CASCADE, games_played int, wins int,
            total_points bigint, best_round int, unique_answers int, updated_at)
user_games (user_id → users ON DELETE CASCADE, game_id uuid, played_at, won bool,
            points int, best_round int, unique_answers int, PK (user_id, game_id))
```

The in-memory store implements the same `store.Accounts` contract (one shared
contract test suite runs against both).

### Privacy

Stored: the Google `sub` (an opaque id), the display name Google sent (cut to 16
characters), the nickname/avatar the user chose to save, timestamps, the stats
above and one row per finished game (id, time, score numbers). **Not stored:**
email, profile picture, locale, access/refresh/ID tokens, IP addresses, answers
or other players' ids. The `profile` scope is requested for the display name
only. `DELETE /api/me` removes
everything about the user at once; logging in again with the same Google account
creates a new, empty one. Room snapshots hold user ids (the player → user links
and the results of games not recorded yet, the outbox) until the room is gone;
recording a result of a user who deleted their account meanwhile is skipped
silently.

**Accepted risk: the guest cookie is the bearer credential of a seat.** Whoever
holds a player's `tf_guest` cookie can take the seat over (that was already so in
Phase 1). With accounts the room freezes the player → user link once the player
took part in a round (see Stats), so a stolen seat cannot be turned into credit
for rounds already played, but it can still play on in the victim's name, and it
earns credit for its own account in a game where the victim never took part (a
seat that has not played yet follows the latest connection). The cookie is
`HttpOnly`, `SameSite=Lax` and `Secure` over https; there is no revocation short
of clearing it.

### Limits and requirements

* Login and callback share one bucket per client IP, effectively global behind
  Traefik: 60/min, burst 30 (a login spends two). Everything else is unchanged.
* Production needs (Secret `tutti-frutti-secrets`, already wired by the chart):
  `google-client-id` and `google-client-secret` together (one without the other
  is a startup error), `session-secret` (≥ 32 bytes) and `PUBLIC_URL`, which the
  server now **requires when Google is enabled** (`config.Load` fails otherwise).
  (The AI judge adds the optional `cloudflare-account-id` / `cloudflare-api-token` keys, see
  section 3, "AI judge".) Without Google credentials the server runs as
  before: `googleEnabled` is false and the login endpoints answer
  `404 auth_disabled`. Changing `SESSION_SECRET` logs everybody out (and makes
  guests new players).
* **Rollbacks.** Rolling the server back to a release older than Phase 4 is safe
  for the rooms (the old binary ignores the new snapshot fields) but it forgets
  the stats outbox: the first snapshot it writes drops the game id and any
  record still waiting for delivery. Records are normally delivered within
  seconds of `game_over`, so this only matters when the database was failing at
  the time; before such a rollback check
  `tuttifrutti_stats_recorded_total{result="error"}` and the server log. The
  stats tables themselves are untouched (the migration is additive).
* **Rolling back past the Dutch release (v0.5.0)** is *not* free: an older
  server only knows the languages `es` and `en`, so it cannot restore a room
  saved with `lang: "nl"` and deletes that snapshot at start-up (the same rule
  that protects against any unreadable snapshot). Rooms saved in Spanish or
  English are unaffected. Before such a rollback, wait until no Dutch game is in
  progress (there is no per-language metric by design, so ask the players), and
  expect any Dutch room that is still open to be gone afterwards. Clients also
  send protocol version 2 (3 since the vote to stop), which the older server refuses,
  so every open tab is asked to reload anyway.
* **Rolling back past the vote to stop** is safe for the rooms: the old binary ignores
  the new `stopVote` fields of the snapshots (the setting is lost when it rewrites them, so
  a game restored by the next release starts with the feature off, and an open vote is gone
  either way), but it speaks protocol version 2 and refuses `v:3`
  (`unsupported_version`, close 4003), so every open tab is told to reload and then runs
  the older client, which knows neither the setting nor the vote. Nothing else needs doing.
* Stats are best effort. A finished game is lost without a `dropped` count only
  when the stats store keeps failing **and** its room closes for good before the
  record is delivered; the `error` counter and the log still show it.

## 10. Assumptions / open tunables

Cluster is amd64. Tunables: English hard-letter list, 120 ms letter speed,
10 s chooser auto-stop, 3–10 category range, 3 s grace, 500 ms late-update
allowance.
