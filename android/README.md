# Tutti Frutti for Android (Trusted Web Activity)

A thin Android shell that opens the live website, <https://tuttifrutti.aresius.xyz>, in a
**Trusted Web Activity** (TWA) so the game can be installed from Google Play. There is no second
frontend: no native game code, no WebView, no JS ↔ native bridge, no native sign-in, no analytics,
ads, push or billing. The web app and the Go server are the product; this module only wraps them.

* Architecture decision and invariants: [`docs/DESIGN.md`](../docs/DESIGN.md), "Android app (TWA)".
* Build, signing, versioning and release runbook: [`docs/ANDROID.md`](../docs/ANDROID.md).
* Google Play checklist and policy gates: [`docs/PLAY-RELEASE.md`](../docs/PLAY-RELEASE.md).

| | |
|---|---|
| Package / applicationId / namespace | `xyz.aresius.tuttifrutti` (permanent once uploaded to Play) |
| Name | "Tutti Frutti", a constant in every language (no localised strings for it) |
| Opens | `https://tuttifrutti.aresius.xyz/` (theme/status/background colour `#1a1033`) |
| Fallback without a TWA provider | Custom Tab (`customtabs`), never a WebView |
| Android | minSdk 29, compileSdk/targetSdk 36 |

## Layout

```
android/
  app/
    build.gradle                    all app configuration (TWA values, versions, signing, lint)
    src/main/AndroidManifest.xml    launcher + verified App Links (paths listed explicitly)
    src/main/java/xyz/aresius/tuttifrutti/
      LauncherActivity.java         validates every incoming URL, then launches the TWA
      LaunchUrlValidator.java       pure, unit-tested URL allow-list
      DelegationService.java        notification delegation: declared but disabled
    src/main/res/                   icons, splash, FileProvider paths, backup rules
    src/test/java/...               JUnit tests (validator + manifest/validator sync)
  scripts/
    generate-icons.mjs              icons + splash from web/static (uses web/node_modules/sharp)
    check-version.mjs (+ test)      versionName / versionCode validation
    audit-manifest.mjs (+ test)     fails on drift of the MERGED manifest
    fixtures/merged-release.xml     a real merged release manifest, input of the audit test
  version-code.txt                  the versionCode (one integer)
  gradlew, gradle/wrapper/          Gradle 8.11.1 wrapper (checked in)
```

## Pinned toolchain

| Component | Version |
|---|---|
| JDK | 17 (Temurin) |
| Gradle | 8.11.1 (`gradle/wrapper`, `distributionSha256Sum` set) |
| Android Gradle Plugin | 8.9.1 (`build.gradle`) |
| Build-tools | the AGP 8.9.1 default, 35.0.0 (downloaded on demand) |
| compileSdk / targetSdk / minSdk | 36 / 36 / 29 |
| `com.google.androidbrowserhelper:androidbrowserhelper` | 2.7.4 |
| `junit:junit` (tests) | 4.13.2 |

Repositories: `google()` and `mavenCentral()` only, declared in `settings.gradle` with
`FAIL_ON_PROJECT_REPOS` (never `jcenter()`). Versions are pinned in the build files; transitive
AndroidX versions are resolved by Gradle (inspect with `./gradlew :app:dependencies`). Gradle
dependency verification / lockfiles are not enabled yet (see "Open points" in `docs/ANDROID.md`).
Renovate proposes bumps for this module (`renovate.json`: `gradle` and `gradle-wrapper` managers, group
"android dependencies"); read the AGP / androidbrowserhelper release notes and wait for `ci-android.yml`
before merging one.

## Common commands

```bash
cd android
./gradlew testDebugUnitTest lintRelease assembleDebug bundleRelease   # what CI runs (unsigned)
node --test scripts/*.test.mjs                                        # script tests (no deps)
node scripts/check-version.mjs                                        # versionName / versionCode
node scripts/audit-manifest.mjs                                       # after bundleRelease
```

Known lint warnings (not errors): `ObsoleteSdkInt` for `mipmap-anydpi-v26` (kept like the
template) and `MonochromeLauncherIcon` (no themed-icon layer yet).

## Provenance: scaffolded from Bubblewrap, maintained by hand

The project was scaffolded from the **Bubblewrap 1.27.0** template
(`packages/core/template_project`, <https://github.com/GoogleChromeLabs/bubblewrap/tree/v1.27.0>) by
resolving its `<%= %>` placeholders by hand. Bubblewrap is **not** a dependency of this repository.
Files derived from the template keep their Apache-2.0 license headers.

**Do not regenerate this project with `bubblewrap init/update`.** It would silently undo the
deviations below (several of them are security decisions). To follow Bubblewrap upstream, diff its
template against these files and port the changes you want.

Intentional deviations from the template:

| Area | Deviation | Why |
|---|---|---|
| Gradle wrapper | Official 8.11.1 wrapper generated with `gradle wrapper --gradle-version 8.11.1` (jar sha256 `2db75c40782f5e8ba1fc278a5574bab070adccb2d21ca5a6e5ed840888448046`, matches Gradle's published checksum) and `distributionSha256Sum` added | The jar inside Bubblewrap 1.27.0 (sha256 `3dc39ad6…`) does not match 8.11.1's published wrapper checksum |
| Repositories / plugins | `settings.gradle` declares `google()` + `mavenCentral()` with `FAIL_ON_PROJECT_REPOS`; AGP via the `plugins` block; no `jcenter()`, no `clean` task | jcenter is shut down; one place to audit repositories |
| `app/build.gradle` | `compileSdk`/`minSdk`/`targetSdk` DSL, Java 17 (template: 1.8), `buildConfig` for `HOST_NAME`, `manifestPlaceholders` for the host | Single source of truth for the host (manifest, launch URL, asset statements, validator) |
| Versions | `versionName` from `helm/tutti-frutti/Chart.yaml` `appVersion`, `versionCode` from `version-code.txt`; the build fails with a clear message otherwise | One website version; Play needs a monotonic integer |
| Signing | Release signed only from `ANDROID_UPLOAD_*` environment variables; unsigned when none is set, error when only some are | No secret on disk or in the repo |
| Lint | `lint { checkReleaseBuilds true; abortOnError true }` (template: `checkReleaseBuilds false`) | Lint must run on release |
| Shortcuts | `generateShorcutsFile` task, `shortcuts.xml`, shortcut drawables/colours, `android.app.shortcuts` meta-data removed; the library's `ShortcutTrampolineActivity` and `ColdShortcutActivity` are removed from the merged manifest (`tools:node="remove"`) | The app has no shortcuts; the exported trampoline would open any same-origin URL and bypass the validator |
| Features not used | No `webManifestUrl`, `fullScopeUrl`, share target, launch handler, protocol/file handlers, `shareTargetIntentFilter`, additional trusted origins, `fileTree(libs)`; no `twa_generator` meta-data, `WebViewFallbackActivity`, empty `Application` class | Not needed; fewer components and no WebView |
| Notifications | `enableNotification=false`: `DelegationService` stays declared but is neither enabled nor exported; no `POST_NOTIFICATIONS` | The website sends no notifications |
| Manifest | `allowBackup="false"` + `dataExtractionRules` + `fullBackupContent` excluding everything; no `package` attribute (the Gradle `namespace` is used); the browsable intent-filter lists **explicit paths** instead of the whole host; scheme and host in separate `<data>` elements (lint) | The shell stores nothing; do not capture `/api`, the OAuth callback or the whole domain |
| `LauncherActivity` | Overrides `getUrlForIntent` and `getLaunchingUrl` through `LaunchUrlValidator`; portrait is set unconditionally (minSdk 29 is above the Oreo guard) | The activity is exported: every URL is untrusted |
| Icons and splash | `generate-icons.mjs` (sharp) from `web/static`; the splash is `logo-mark.svg` centred on a transparent square instead of the 512 icon | The splash screen paints `#1a1033` behind it; reproducible assets |
| Site settings | `ManageDataLauncherActivity` (not exported) and `android:manageSpaceActivity` are KEPT from the template | Required: the library's `LauncherActivity` always registers a "Site settings" shortcut for it and the app crashes on launch without it (`IllegalArgumentException: Component class ... does not exist`, seen on Android 15); `audit-manifest.mjs` checks it is declared |
| `enable-debug.sh` | Not carried over; the `adb` command is in `docs/ANDROID.md` | Debug-only helper |
| Added | JUnit tests, `check-version`, `audit-manifest`, `ci-android.yml`, `release-android.yml` | Verifiable, reproducible shell |
