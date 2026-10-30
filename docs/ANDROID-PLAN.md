# Android / Google Play plan

Status tracker and implementation contract for publishing Tutti Frutti on Google Play
while keeping the website. The architecture decision and invariants live in
[`DESIGN.md`](DESIGN.md) ("Android app (TWA)"); the build and release runbooks live in
[`ANDROID.md`](ANDROID.md) and [`PLAY-RELEASE.md`](PLAY-RELEASE.md); report handling lives
in [`MODERATION.md`](MODERATION.md).

## Decisions (owner, October 2026)

| Area               | Decision                                                                     |
| ------------------ | ---------------------------------------------------------------------------- |
| First release      | The existing online game; no new offline game mode                           |
| Technology         | Trusted Web Activity (TWA) opening the live website; no Capacitor, no WebView |
| Website            | Stays available and installable as a PWA; one codebase, one backend           |
| Audience           | 13+ in Argentina and the Netherlands (gate G1: recommended, owner confirms in the Play form) |
| Play countries     | Argentina and the Netherlands first; the website stays reachable everywhere   |
| Developer account  | New personal account (aresiusxp@gmail.com), identity verification in progress |
| Package ID         | `xyz.aresius.tuttifrutti` (permanent once uploaded)                           |
| Gameplay           | Keep private rooms, public rooms and the AI judge                             |
| Safety             | Terms acceptance, reporting, blocking, the moderation process in `MODERATION.md` (gate G4 resolved) |
| Support links      | Hidden inside the Android app, kept on the website; never in the listing (gate G2, final) |
| Not in scope       | Ads, paid features, billing, analytics SDKs, push, native auth, offline game |

## Invariants

* One origin (`https://tuttifrutti.aresius.xyz`), one backend. No CORS relaxation, no cookie
  bridge, no native Google sign-in, no tokens exposed to JavaScript.
* The Android shell stores nothing. Browser data belongs to the browser hosting the TWA.
* The engine stays pure; moderation I/O lives outside the game engine and outside actors.
* Web and server changes ship through the existing GitOps flow; only shell changes
  (permissions, SDK targets, icons, link filters) need a Play release.
* Nothing in the repository claims legal compliance. Policy questions are release gates.

## Batches

Run `@review` after each batch. Do not use the CodeRabbit CLI in this project.

| #   | Batch                                                                 | Status |
| --- | --------------------------------------------------------------------- | ------ |
| 1   | Docs, Android scaffold, CI (`android/`, workflows, ANDROID/PLAY docs) | [x] built, reviewed; CI and device runs pending |
| 2   | Asset Links + nginx, update coordinator, Back/modal handling          | [x] done; real fingerprint pending (owner) |
| 3   | Terms and reporting (server foundation, web gate and report UI)       | [x] done, reviewed |
| 4   | Blocking, restrictions and operator moderation CLI                    | [x] server done; web UI with batch 3 |
| 5   | Resume recent room                                                    | [x] done; real-device process-death run pending |
| 6   | Deletion hardening, privacy/terms/deletion pages, support-link decision | [x] done (G2 final: hidden in the app); real-device check pending |
| 7   | Final validation, listing, closed test, production                    | [~] automated validation + listing assets done; moderation commitments and alerts done (G4 resolved); G1 recommended; device tests, closed test (plan in PLAY-RELEASE.md, section 6) and publication are owner steps |

## Owner gates (cannot be decided by code)

Status of the gates of [`PLAY-RELEASE.md`](PLAY-RELEASE.md), section 9:

1. Play Console: finish identity and device verification, create the app, obtain the
   **Play app-signing SHA-256** and put it in `web/static/.well-known/assetlinks.json`; declare the trader
   status (recommended: not a trader).
2. Closed test (G7): 12 or more genuine testers opted in for 14 continuous days; plan (Google Groups,
   unsynced closed-track countries, what to ask testers, AppHive limits) in PLAY-RELEASE.md, section 6.
3. Regional minors/consent (G1): **recommended** 13+ in Argentina and the Netherlands, age groups 13-15,
   16-17 and 18+, contract and legitimate interest as legal bases; the owner confirms in the Target audience form.
4. Cafecito/Ko-fi (G2): **final**: hidden inside the Android app, kept on the website, never in the listing.
5. Terms/community rules wording (G3): open. Response commitments, appeals and escalation (G4): **resolved**
   in `MODERATION.md` ("Commitments": the same figures as the terms' *Our response times*, the privacy policy and
   the deletion page, including the Argentine deadlines of Ley 25.326 for data requests and deletion by email).
6. Real-device test matrix (see PLAY-RELEASE.md) and the internal-track Play-signed install (G6).

## Test matrix (release blocking)

* Automated: `go build ./... && go vet ./... && go test -race ./...`, `go tool tygo generate`
  (no drift), `npm run check && npm run lint && npm run test && npm run build`, Helm lint,
  Android unit tests/lint/bundle in CI, e2e suite.
* Manual on real Android devices: verified links (cold/warm, lowercase, expired/full rooms),
  Google login/cancel/logout/deletion, process death and resume, first launch offline,
  Back/keyboard/modals, font scaling, TalkBack, cutouts, gesture and 3-button navigation,
  screen lock, battery saver, Wi-Fi/mobile switches, wake-lock refusal, browser and Android
  players in the same game, rolling-deploy overlap.
