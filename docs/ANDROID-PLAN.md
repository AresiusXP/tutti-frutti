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
| Audience           | 13+ (regional minors/consent rules are a release gate, see below)             |
| Play countries     | Argentina and the Netherlands first; the website stays reachable everywhere   |
| Developer account  | New personal account (aresiusxp@gmail.com), identity verification in progress |
| Package ID         | `xyz.aresius.tuttifrutti` (permanent once uploaded)                           |
| Gameplay           | Keep private rooms, public rooms and the AI judge                             |
| Safety             | Terms acceptance, reporting, blocking, a small moderation process            |
| Support links      | Keep Cafecito/Ko-fi only if they qualify for Play's no-perk direct-tip rule   |
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
| 6   | Deletion hardening, privacy/terms/deletion pages, support-link decision | [x] done; real-device check pending |
| 7   | Final validation, listing, closed test, production                    | [~] automated validation + listing assets done; device tests, closed test and publication are owner steps |

## Owner gates (cannot be decided by code)

1. Play Console: finish identity and device verification, create the app, obtain the
   **Play app-signing SHA-256** and put it in `web/static/.well-known/assetlinks.json`.
2. Recruit more than 12 genuine closed-test testers (12 opted in for 14 continuous days).
3. Regional minors/consent determination for Argentina and the Netherlands at 13+.
4. Cafecito/Ko-fi: validate the Play direct-tip exception against the real payment flows;
   if uncertain, omit the links from the Android experience (the website keeps them).
5. Final wording of the terms/community rules, response commitments for reports, appeals.
6. Real-device test matrix (see PLAY-RELEASE.md) and the internal-track Play-signed install.

## Test matrix (release blocking)

* Automated: `go build ./... && go vet ./... && go test -race ./...`, `go tool tygo generate`
  (no drift), `npm run check && npm run lint && npm run test && npm run build`, Helm lint,
  Android unit tests/lint/bundle in CI, e2e suite.
* Manual on real Android devices: verified links (cold/warm, lowercase, expired/full rooms),
  Google login/cancel/logout/deletion, process death and resume, first launch offline,
  Back/keyboard/modals, font scaling, TalkBack, cutouts, gesture and 3-button navigation,
  screen lock, battery saver, Wi-Fi/mobile switches, wake-lock refusal, browser and Android
  players in the same game, rolling-deploy overlap.
