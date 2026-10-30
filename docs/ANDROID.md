# Android app (Trusted Web Activity)

How the Android app is built, signed, verified, versioned and released. The architecture decision and
its invariants are in [`DESIGN.md`](DESIGN.md) ("Android app (TWA)"); the Play Console checklist and
the open policy decisions are in [`PLAY-RELEASE.md`](PLAY-RELEASE.md); the module itself is documented
in [`../android/README.md`](../android/README.md). Nothing here claims legal or policy compliance:
open questions are listed as owner decisions in `PLAY-RELEASE.md`.

## 1. Architecture and why a TWA

The Android app is a thin shell that opens `https://tuttifrutti.aresius.xyz/` in a **Trusted Web
Activity**: full-screen Chrome (or another TWA-capable browser) rendering the real website, with no
browser UI once the origin is verified. The shell contains no game code.

Why this and not something else:

* **One product, one origin, one backend.** The same SvelteKit build and Go server serve the browser,
  the installable PWA and the Android app. A web release reaches Android users the moment it is
  deployed (GitOps), with no Play review.
* **Google sign-in works.** Google refuses OAuth in embedded WebViews. A TWA runs in the real browser,
  so the existing authorization-code + PKCE flow, the `tf_session` cookie and its `SameSite=Lax`
  semantics are untouched. No native auth, no cookie bridge, no tokens exposed to JavaScript.
* **Nothing to keep in sync.** No Capacitor/React Native/Kotlin rewrite, no second frontend, no
  JS ↔ native bridge, no WebView (the fallback is a Custom Tab).
* **A small attack surface.** No permissions, no backup, no analytics/ads/Firebase/push/billing, one
  exported activity that validates every URL it is given.

## 2. Layout

See [`../android/README.md`](../android/README.md) for the file tree, the pinned toolchain and the list of
deviations from the Bubblewrap template the project was scaffolded from (maintained by hand: **never**
regenerate it with Bubblewrap).

Key files: `android/app/build.gradle` (configuration), `android/app/src/main/AndroidManifest.xml`,
`LaunchUrlValidator.java` (the URL allow-list), `android/version-code.txt`,
`android/scripts/*.mjs`, `.github/workflows/ci-android.yml`, `.github/workflows/release-android.yml`.

## 3. Behaviour of the shell

* **Name** "Tutti Frutti" in every language; theme, status bar, navigation bar and splash colour
  `#1a1033` (the web theme colour); portrait; fallback `customtabs`; notification delegation off.
* **Verified App Links** (`android:autoVerify="true"`, `https`, host `tuttifrutti.aresius.xyz`) only for:
  `/`, `/rooms`, `/stats`, `/privacy`, `/terms`, `/delete-account` (exact `android:path`) and room
  invites (`android:pathPattern="/...."`, a slash plus exactly 4 characters). `/api`, `/ws` and
  `/api/auth/google/callback` are **not** captured, nor is the rest of the domain. A glob cannot express
  the room-code alphabet, so any 4-character top-level path (`/docs`) reaches the activity, which then
  falls back to the home page.
* **`LaunchUrlValidator`** (pure Java, JUnit-tested) decides what `LauncherActivity` may open, because
  the activity is exported and any app can send it an intent. Accepted: scheme `https`, host exactly
  `tuttifrutti.aresius.xyz` (no userinfo, no port except 443, no trailing dot, no look-alike), and a path that
  is one of the pages above or `/` + 4 characters of the server's room-code alphabet
  `BCDFGHJKLMNPQRSTVWXZ23456789` (any ASCII case, like `web/src/params/roomcode.ts`). The URL that is opened
  is **rebuilt from constants**: the query and fragment are dropped, a lowercase room code is
  upper-cased (ASCII only); a **path** that contains percent-encoding, `..`, backslashes, a trailing slash or
  non-ASCII characters is rejected. Anything else opens `https://tuttifrutti.aresius.xyz/`. The library's "original launch URL" is
  sanitised the same way, and the library's exported shortcut trampoline is removed from the manifest.
* **No storage, no backup**: `allowBackup="false"`, backup/extraction rules exclude everything. Cookies
  and `localStorage` belong to the browser that hosts the TWA.
* **No permissions.** The merged manifest only contains the signature-level
  `<package>.DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION` that AndroidX adds. `scripts/audit-manifest.mjs`
  fails the build on any other `<uses-permission>` (INTERNET included), on `allowBackup` other than
  `false`, on a web intent-filter without exact path restrictions or with a catch-all, and on any
  exported component outside its allow-list.

### Resume after the browser process dies

Android may kill the browser that hosts the TWA while it is in the background; the per-tab room marker is lost with it.
The web app keeps one recent room in `localStorage['tf.recentRoom']` (`{code, roomInstanceId, lastJoinedAt}`, 24 h) and Home
offers **Resume recent game** (DESIGN.md, 9a). It never takes a seat on its own and never stores answer drafts: what the
server already received survives, **text typed but not sent can be lost** when the process dies.

## 4. Local setup

Requirements: JDK 17 (Temurin), the Android SDK, Node 24 (scripts only).

```bash
# JDK 17 (macOS example; any Temurin 17 works)
brew install --cask temurin@17
export JAVA_HOME="$(/usr/libexec/java_home -v 17)"

# Android SDK command-line tools: unzip commandlinetools-<os>_latest.zip from
# https://developer.android.com/studio#command-tools into $ANDROID_HOME/cmdline-tools/latest
export ANDROID_HOME="$HOME/Library/Android/sdk"        # or any directory you own
yes | "$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" --licenses
"$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager" "platforms;android-36" "build-tools;35.0.0"
```

Alternatively install Android Studio and open `android/`. If `ANDROID_HOME` is not set, put
`sdk.dir=/path/to/sdk` in `android/local.properties` (git-ignored). Gradle downloads the build-tools
AGP wants if they are missing and the licenses are accepted.

Icons and splash are generated, not hand-drawn: `cd web && npm ci && cd ../android && node
scripts/generate-icons.mjs` rewrites `res/mipmap-*` and `res/drawable-*` from `web/static/` (commit the
PNGs). It uses `sharp` from `web/node_modules`; `web/package.json` is not touched.

## 5. Build

```bash
cd android
./gradlew testDebugUnitTest lintRelease assembleDebug bundleRelease   # = CI, release bundle UNSIGNED
```

| Output | Path |
|---|---|
| Debug APK (debug keystore) | `app/build/outputs/apk/debug/app-debug.apk` |
| Release bundle (unsigned unless the signing variables are set) | `app/build/outputs/bundle/release/app-release.aab` |
| Merged release manifest (audited) | `app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml` |
| Lint report | `app/build/reports/lint-results-release.html` |

Then `node --test scripts/*.test.mjs`, `node scripts/check-version.mjs` and `node scripts/audit-manifest.mjs
--expect-version-name <v> --expect-version-code <n>`.

A signed local build reads **only** these environment variables (all four or none; some but not all is
an error): `ANDROID_UPLOAD_KEYSTORE_PATH`, `ANDROID_UPLOAD_KEYSTORE_PASSWORD`, `ANDROID_UPLOAD_KEY_ALIAS`,
`ANDROID_UPLOAD_KEY_PASSWORD`. Never put them in a file in the repository.

`jarsigner -verify app-release.aab` exits 0 even for an **unsigned** file: read its output (`jar
verified.` versus `jar is unsigned.`). The release workflow does.

## 6. Signing model

* **Play App Signing.** Google holds the key that signs what users install. The app-signing key never
  leaves Google; its certificate SHA-256 is what the website's `assetlinks.json` must list (section 7).
* **Upload key.** The owner holds a separate key; only it signs the `.aab` that is uploaded to Play. If it
  is lost or leaked, Play support can reset it (Play Console > Test and release > App signing > request
  upload key reset); the app-signing key and the installed base are unaffected.
* **Where it lives.** Offline (password manager / encrypted backup) and, for the release workflow, as
  secrets of the GitHub environment `android-release`. It is never committed and never created by an
  agent or CI.

Create the upload key once, by hand (instructions only; choose strong passwords and keep two backups):

```bash
keytool -genkeypair -v \
  -keystore upload-keystore.jks -storetype PKCS12 \
  -alias upload -keyalg RSA -keysize 2048 -validity 9125 \
  -dname "CN=Tutti Frutti upload key"

# the certificate Play Console asks for (upload the PEM there, or read the SHA-256 below)
keytool -exportcert -rfc -keystore upload-keystore.jks -alias upload -file upload-certificate.pem
keytool -list -v -keystore upload-keystore.jks -alias upload | grep 'SHA256:'
base64 -i upload-keystore.jks | tr -d '\n' | pbcopy      # Linux: base64 -w0 upload-keystore.jks
```

GitHub: Settings > Environments > **`android-release`**: required reviewers (the owner), deployment
branches `main`; secrets `ANDROID_UPLOAD_KEYSTORE_B64` (the base64 above), `ANDROID_UPLOAD_KEYSTORE_PASSWORD`,
`ANDROID_UPLOAD_KEY_ALIAS`, `ANDROID_UPLOAD_KEY_PASSWORD`; optional **variable** `ANDROID_UPLOAD_CERT_SHA256`
(the upload certificate's fingerprint, public) which the signed bundle must match.

## 7. Digital Asset Links (what makes it a TWA)

The browser only hides its UI, and Android only auto-verifies the links, when the website vouches for the
app at `https://tuttifrutti.aresius.xyz/.well-known/assetlinks.json`. That file belongs to the website
(`web/static/.well-known/assetlinks.json`, served with `Content-Type: application/json`, no redirect); this
repository's Android side only needs the right fingerprint in it:

```json
[{
  "relation": ["delegate_permission/common.handle_all_urls"],
  "target": {
    "namespace": "android_app",
    "package_name": "xyz.aresius.tuttifrutti",
    "sha256_cert_fingerprints": ["<APP-SIGNING SHA-256 FROM PLAY CONSOLE>"]
  }
}]
```

How to get the fingerprint:

1. Create the app in Play Console and enrol in Play App Signing (the default for new apps), then upload a
   first bundle to **internal testing** (the key is created on the first upload).
2. Play Console > your app > **Test and release > App signing** (older consoles: Release > Setup > App
   signing) > "App signing key certificate" > copy the **SHA-256 certificate fingerprint**
   (`AA:BB:...`, 32 pairs).
3. Put it in `sha256_cert_fingerprints` (the website agent/owner edits the file), deploy the website, and
   verify (section 8). Keep the entry for as long as any installed version is signed with that key.

Do **not** add the debug keystore's fingerprint to the production file. Debug builds fall back instead
(section 9). Adding the upload key's fingerprint is only useful for sideloading release builds you signed yourself
with it; Play-delivered installs are signed with the app-signing key.

## 8. Verifying on a device

```bash
adb install -r app/build/outputs/apk/debug/app-debug.apk           # or install from Play internal testing
adb shell pm get-app-links xyz.aresius.tuttifrutti                  # tuttifrutti.aresius.xyz: verified
adb shell pm verify-app-links --re-verify xyz.aresius.tuttifrutti   # ask Android to retry now
adb shell am start -a android.intent.action.VIEW -d "https://tuttifrutti.aresius.xyz/KT7P"
adb shell am start -a android.intent.action.VIEW -d "https://tuttifrutti.aresius.xyz/api/session"   # must NOT open the app
```

Expect `verified` for a Play-signed install after `assetlinks.json` is live. The statement itself can be checked
without a device:
<https://digitalassetlinks.googleapis.com/v1/statements:list?source.web.site=https://tuttifrutti.aresius.xyz&relation=delegate_permission/common.handle_all_urls>
must list `xyz.aresius.tuttifrutti` with the app-signing SHA-256. Also open
`https://tuttifrutti.aresius.xyz/.well-known/assetlinks.json` in a browser (HTTP 200, JSON, no redirect).

## 9. Debug builds and the fallback

A debug APK is signed with the debug keystore, whose fingerprint is not in `assetlinks.json`, so Chrome cannot
verify it and the launcher opens the site in a **Custom Tab with a URL bar**. That is acceptable for debug: the
page, the cookies and the game work the same. To see the real full-screen TWA with a debug build:

1. On the phone, Chrome > `chrome://flags` > enable "Enable command line on non-rooted devices", relaunch.
2. `adb shell "echo '_ --disable-digital-asset-link-verification-for-url=\"https://tuttifrutti.aresius.xyz\"' > /data/local/tmp/chrome-command-line"`
3. Force-stop Chrome and start the app. (Remove the file when done: `adb shell rm /data/local/tmp/chrome-command-line`.)

The same Custom-Tab fallback is what users without a TWA-capable browser get.

## 10. Versioning

* **versionName** = `appVersion` in `helm/tutti-frutti/Chart.yaml` (bumped by release-please, the one
  version of the repo). The build fails with a clear message if the line is missing.
* **versionCode** = the single positive integer in `android/version-code.txt` (starts at 1, at most
  2100000000). It is bumped **by hand**, +1, in the PR that prepares an Android release, never from the date or
  the CI run number. Play rejects a versionCode it has already seen and users only update to a higher one:
  keep it strictly increasing.
* `scripts/check-version.mjs` validates the file format, that the versionName is semantic, and with
  `--min-exclusive N` that versionCode > N (N = the previous Play release). `release-android.yml` runs it with
  `--expect-version <tag>` and `--min-exclusive <previous_version_code>`, and `audit-manifest.mjs` checks that the
  built manifest carries the same two numbers.
* Web-only releases do **not** need an Android release: the website updates itself. The Android build of a
  tag uses that tag's `version-code.txt`, so the bump must be merged **before** the release PR that creates the
  tag. Tags are immutable: to ship a shell change the next release must contain it. Use a releasable
  conventional commit, `fix(android): ...` or `feat(android): ...` (`chore:`, `ci:`, `build:` do not cut a release).

## 11. Release runbook

1. Decide whether Play needs a release at all (section 13). If not, nothing to do.
2. In a PR: change the shell, bump `android/version-code.txt`, keep `ci-android` green. Merge, then merge the
   release-please PR: that creates `vX.Y.Z` (and deploys the website as usual).
3. Actions > **Release — Android** > Run workflow (from `main`): `tag` = `vX.Y.Z`, `previous_version_code` =
   the last uploaded versionCode (empty only for the very first release). Approve the `android-release`
   environment. The workflow refuses a malformed tag, a tag not on main, a Chart.yaml that does not match the tag
   and a stale versionCode; runs the tests, release lint and the manifest audit without any secret; then
   builds the signed bundle, verifies it with `jarsigner -verify` (and against `ANDROID_UPLOAD_CERT_SHA256` when
   set), removes the keystore and uploads `tutti-frutti-vX.Y.Z-codeN.aab`, its `.sha256` and
   `build-metadata.json`. It **never publishes** to Google Play.
4. Download the artifact, check `sha256sum -c`, upload the `.aab` to **Internal testing** in Play Console (see
   [`PLAY-RELEASE.md`](PLAY-RELEASE.md)), install from the Play link on a real device and run the manual matrix
   before promoting.
5. Record the new versionCode as the next `previous_version_code`.

The Android workflows are independent of `release.yml` and `docker-build.yml`: an Android failure cannot block
or alter a website release.

## 12. Rollback

* **Website:** unchanged, GitOps. Revert the version bump in `argocd-apps`; every Android user sees the
  previous website immediately because the app is the same origin.
* **Android:** Play cannot roll an installed version back. Halt a staged rollout in Play Console (Production >
  Releases > Halt rollout), then ship the previous code as a **new** build with a **higher** versionCode.
* **Keep `assetlinks.json` valid** through every rollback: never remove or replace the app-signing
  fingerprint while versions signed with it are installed, or those installs lose full-screen mode and App Links
  verification (they degrade to a Custom Tab with a URL bar).

## 13. What needs a Play release and what ships with the website

| Change | Ships via |
|---|---|
| Any web UI, game, server, translation, privacy/terms/deletion page content | Website (GitOps) |
| `assetlinks.json` fingerprints | Website |
| Store listing text/screenshots, Data safety and other Play Console declarations | Play Console (its own review), no build |
| New top-level page that must open **from other apps' links** into the app | Play release (intent-filter path + `LaunchUrlValidator` + their tests) |
| Name, launcher icon, splash, colours, orientation, fallback strategy | Play release |
| Host/domain, launch URL, package id (never changes) | Play release (the domain also needs a new `assetlinks.json`) |
| Permissions, SDK/AGP/library upgrades, target SDK, backup rules | Play release |

Links *inside* the running app stay in the app whatever the intent-filter says; the filter only controls links
tapped in other apps.

## 14. Platform checks to repeat every year

* **Target API level.** Google Play requires new apps and updates to target a recent API level and moves the bar
  every year (usually enforced from end of August). Check "Target API level requirements" in Play Console and
  <https://support.google.com/googleplay/android-developer/answer/11926878>; raise `compileSdk`/`targetSdk`
  in `app/build.gradle` (and AGP if needed), run the full CI, and ship a Play release.
* **16 KB page size.** Apps with native code must support 16 KB memory pages (see
  <https://developer.android.com/guide/practices/page-sizes>). This app has **no native libraries**:
  `unzip -l app-release.aab | grep '\.so'` must print nothing, and Play Console's app-bundle explorer should
  show no 16 KB warning. Re-check after every dependency upgrade.
* **Dependencies.** `androidbrowserhelper` (check its release notes for security fixes), AGP, Gradle and the JDK
  are proposed by Renovate (`gradle` and `gradle-wrapper` managers, grouped as "android dependencies"), but a
  bump is merged only after `ci-android.yml` is green and the release notes (AGP, androidbrowserhelper) were read.
  A `targetSdk` raise is a manual change that follows the yearly Play requirement.

## 15. Open points

* Gradle dependency verification (`gradle/verification-metadata.xml`) or dependency locking is not enabled:
  versions are pinned, transitive ones are not hash-checked. The platform-specific tool artifacts make a
  portable metadata file non-trivial; revisit with a CI-generated file.
* No themed (monochrome) adaptive icon layer (lint warning `MonochromeLauncherIcon`).
* Renovate only proposes version bumps: it does not know Play's yearly target-API requirement, so the `targetSdk`
  deadline is on the owner's yearly checklist (section 14).
* The wrapper jar is checked in; CI validates it against Gradle's published checksums on every run.
