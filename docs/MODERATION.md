# Moderation runbook

How the owner of Tutti Frutti (one person, a hobby, in the Netherlands) handles reports, restrictions,
takedowns and the contact mailbox. The design is in [`DESIGN.md`](DESIGN.md) section 9c ("Content safety");
this is the operator's procedure. It gives no legal advice and claims no legal compliance (see "Limits").

Players can report a player (nickname/avatar), a custom category label, an answer or an AI verdict, and
can block another player. There is **no public admin page**: moderation is a command run by the owner
inside the server's pod. The game carries **no images**: avatars are an emoji and a colour, everything else
is text typed by players.

## Commitments

What the owner, a single operator, commits to. The terms page (section "Our response times"), the privacy
policy and the account-deletion page state the same figures in all three languages: change them together, and
never promise more there (gate G4 in [`PLAY-RELEASE.md`](PLAY-RELEASE.md)). The contact address is
**aresiusxp@gmail.com** (the address on the terms and privacy pages).

| What | Commitment |
|---|---|
| In-game reports | Checked **daily** (a goal: see "Absence"); every report decided **within 7 days** of being filed |
| Urgent safety cases: threat to someone's life, self-harm, grooming, sexual content involving a minor | Acted on **within 24 hours of being seen** (see "Urgent safety cases") |
| Notices of illegal content by email | Acknowledged **within 3 days**, decided **within 7 days**; the reply gives the outcome and how to appeal |
| Appeals (of a restriction or a decision about a notice) | Decided **within 14 days** |
| Data-protection requests (access, correction, deletion, a copy, objection) | Answered **without undue delay, at the latest within one month**; where the law allows it (not in Argentina) a complex request can take two more months, and the person is told so within the first month. **Argentina** (Ley 25.326, arts. 14 and 16): access **within 10 days**, correction or deletion **within 5 business days** |
| Account deletion by email (the server stores no email: the reply asks the person to show the account is theirs, for example by signing in) | Answered **without undue delay, at the latest within one month**; the account is deleted **within one month at the latest after** the person has shown it is theirs. **Argentina** (Ley 25.326, art. 16): answered **within 5 business days** and deleted **within 5 business days after** they have shown it is theirs |
| Any other contact | Acknowledged **within 7 days** |
| Removal orders of the Dutch ATKM | Within the legal deadline: **1 hour** for terrorist content, **12 hours** for child sexual abuse material (see "Removal orders") |

## Alerts on the phone

Alertmanager routes the chart's alerts by severity: `critical` to the receiver `ntfy-critical`, `warning` to
`ntfy-warning`; both reach the owner's phone through ntfy. There is no mail or other channel. Two alerts are
about moderation (definitions: [`DESIGN.md`](DESIGN.md), "Observability"):

| Alert | Severity | Fires when | What to do |
|---|---|---|---|
| `TuttiFruttiUrgentReport` | critical | a report with the reason `violence` (violence or threats), `self_harm`, `sexual` or `child_safety` (a child at risk) was filed in the last 10 minutes; one alert per kind and reason | **Open it now**: `moderate reports list`, then `moderate reports show ID`. Real danger: "Urgent safety cases". Otherwise triage it like any report, today |
| `TuttiFruttiReportWaiting` | warning | the oldest open report is older than 20 hours (for 15 minutes) | The daily check is due: run it (next section). It resolves once no open report is older than 20 hours |

`TuttiFruttiUrgentReport` resolves by itself 10 minutes after the last such report: the **open report is the
to-do, not the alert**. The reason is what the reporter picked, so it can be wrong or abusive (an `UNVERIFIED`
report can come from anybody who knows a room code); read it anyway. The other critical alerts
(`TuttiFruttiServerDown`, `TuttiFruttiServerNotReady`) also mean that `moderate` cannot work until the server
is back. Keep the ntfy app allowed to notify (not muted, not battery-restricted) and send it a test push after
changing phones.

## The daily check

The goal is once a day, at about the same time, so `TuttiFruttiReportWaiting` rarely needs to remind you.
The dashboard (row *Accounts*: *Open reports*, *Oldest open report*) shows the same backlog. The routine:

```sh
kubectl -n <namespace> exec deploy/<server> -- /moderate status
```

```
server version:       0.9.0
open reports:         2
oldest open report:   26h0m0s (filed 2026-10-29T10:00:00Z)
active restrictions:  1
blocks:               14
AI judge:             on
public games:         on
```

If `open reports` is 0 you are done. Otherwise triage them (next section). If the command cannot reach
the control socket it says so (the server is down, or the socket could not be created: see the server
log, `moderation control socket`): fix that first, because nobody can be moderated meanwhile. `public
games: OFF` after you are back means you still have to run `moderate public enable` (see "Absence").

A report is **deleted 90 days after it was filed even if nobody looked at it** (30 days after you resolve
it). A report that has waited weeks is about to disappear.

## Access and the real boundary

The control socket (`MODERATION_SOCKET`, chart `server.moderation.socket`, default
`/run/tutti/moderation.sock` on an in-memory `emptyDir`) is created by the server with mode `0600` under
its own user, is not a port, and is in no Service or Ingress. The server never changes the mode or the
owner of the directory it finds (it only creates one that is missing, `0700`): the chart mounts the
`emptyDir` and sets the pod's `fsGroup` to the server's group so the directory is writable by it. If the
socket cannot be created the server keeps playing and logs one `ERROR` that names the path, the uid it runs
as and the owner and mode of the directory (and `moderate status` then says it cannot connect). The `moderate` binary is in the server image
(`/moderate`, the image has no shell) and needs **no database credentials**: the running server executes
every action. So the real access boundary is the right to `kubectl exec` into that pod (Kubernetes
RBAC). The `--operator NAME` you pass is only a label for the audit log (anyone who can exec can type any
name); it is required for every command that changes something.

Flags go **before** the arguments of a command (`reports resolve --note "..." ID`, not `ID --note`);
`moderate help` prints the usage. `--json` prints the raw answer, `--socket PATH` or `$MODERATION_SOCKET`
choose another socket. Everything a player or you typed is printed **quoted, with control and invisible
characters escaped**, so a nickname cannot drive your terminal.

## Triage

```sh
moderate reports list                       # open reports, oldest first (--status open|resolved|all, --limit N)
moderate reports show 5c1d…                 # one report in full
```

What a report holds (`reports show`):

* **Evidence** (`VERIFIED`): the exact text the room copied from the *reporter's own view* (an answer, a
  label, a nickname, the AI verdict and its reason), the language, phase, round, letter and category, and
  who the room knows submitted it (guest id, and account id if the author was signed in). A player cannot
  report an answer they were not shown, and cannot name a target.
* **Evidence `UNVERIFIED`**: when the room was gone, its code was reused, the content had changed, or it
  was never visible to the reporter, the report is still taken, but the evidence is only what the reporting
  client says it saw. It names **no target** (`restrict --report` refuses it) and must never be acted on
  alone. Note that anybody who knows a room code can send a roomInstanceId that does not match and so file
  an `UNVERIFIED` report: it costs them the same rate limits as any report, and `reports list` and
  `reports show` print `UNVERIFIED` for it. Treat it as a hint to look for corroborating, `VERIFIED` reports.
* **Provenance**: room instance, match instance, content key (reports about the same content share it),
  reporter and target ids.
* **The reporter's comment**: free text typed by the reporter, always `UNVERIFIED`.
* The date the report will be deleted.

Decide, then act:

| Situation | Command |
|---|---|
| Nothing wrong | `moderate reports resolve --operator you --note "fine" --dismiss ID` |
| Content is bad and the room is still running | `moderate room close --operator you --instance ROOMINSTANCE` (the instance id is in the report) |
| A nickname/avatar on an account is bad | `moderate profile remove --operator you --user USERID` |
| A person keeps misbehaving | `moderate restrict --operator you --report ID --reason "..." [--days N]` |
| Done | `moderate reports resolve --operator you --note "restricted 14 days, label gone" ID` |

A report you resolve is `actioned` (default) or `dismissed` (`--dismiss`); the note goes into the audit
log, not to the reporter. Resolving twice is refused (it would move the retention clock).

**Reports never punish by themselves.** No number of reports, from anybody, triggers any automatic
action. Every restriction is a decision you take after reading the evidence.

## Commands

| Command | What it does |
|---|---|
| `status` | open reports, age of the oldest, restrictions in force, number of blocks (a count, never who blocked whom), AI judge state, public games state |
| `reports list [--status open\|resolved\|all] [--limit N]` | summaries, oldest first for `open`, newest first otherwise |
| `reports show ID` | the whole report, see above |
| `reports resolve --operator NAME --note TEXT [--dismiss] ID` | closes a report; audited |
| `restrict --operator NAME (--guest ID \| --report ID) [--user ID] --reason TEXT [--days N]` | see "Restrictions" |
| `unrestrict --operator NAME ID` | lifts a restriction (the id is printed by `restrict`) |
| `room close --operator NAME [--instance ID] [CODE]` | closes exactly that room instance |
| `profile remove --operator NAME --user ID` | deletes the saved profile text of an account |
| `judge status` / `judge enable\|disable --operator NAME` | the AI judge's kill switch |
| `public status` / `public enable\|disable --operator NAME` | public games on or off (see "Absence") |

Examples:

```sh
moderate restrict --operator ana --report 5c1d… --user 3f9a… --reason "slurs in custom labels, 3rd report" --days 30
moderate restrict --operator ana --guest 8Qm… --reason "spam nicknames"          # until you lift it
moderate unrestrict --operator ana 7e02…
moderate room close --operator ana KT7P
moderate room close --operator ana --instance 2c9b…     # closes that instance only, wherever it is
moderate room close --operator ana --instance 2c9b… KT7P  # the room holding KT7P must be that instance
moderate judge disable --operator ana
moderate public disable --operator ana
```

## Enforcement semantics

All of it runs in the live server and survives restarts (the records are in Postgres, loaded before the
server accepts a single join).

* **Block** ("Block and leave", made by a player in the app). Keyed by the player's guest id. The blocker
  is removed from every live room where the blocked player is seated, and the two can never be seated in
  the same room again, in either direction, while the block stands; a public room that holds a human the
  caller blocked (or who blocked the caller) is hidden from their directory and shows as not joinable.
  Nothing is told to the blocked player (a refused join says only `join_unavailable`). **First release:
  blocks follow the browser's guest cookie.** Clearing cookies, another browser or another device evades
  a block, and nothing tries to prevent that (no fingerprinting, no cross-device promise). Blocks never
  count against anybody and never expire; they last until the blocker unblocks (an unblock never puts
  anybody back in a room). Limit: 100 per player.
* **Restriction.** A restricted guest cannot create a room (`403 join_unavailable`) nor join or
  reconnect (`join_unavailable` on the socket), and is removed from the rooms it is in when you apply
  it (it is flagged as having left a started game: scores stay). With `--days N` it ends by itself; without,
  it lasts until `unrestrict`. `--report ID` takes the target from the report (a report without a verified
  target is refused: use `--guest` only if you are sure who it is). `--user ID` also restricts the account,
  but **only** with `--report`, and only the account that report proved sat behind the guest (the server
  cannot otherwise know which account is behind a guest id). Accounts are Google sign-ins seen only as an
  id: there is no email to contact. A restricted account is refused even from another browser while signed
  in; the same person signed out and in another browser is a new guest identity and is not caught.
  There is at most one active restriction per guest, also when two `restrict` commands race: the database
  transaction checks and inserts under a per-guest lock, and the second one is refused as
  `already_restricted` with nothing stored and nothing audited.
* **Resolving a report** is a compare-and-set on the open status, in the same transaction as its audit
  record: if two `reports resolve` race (or you run it twice), exactly one wins, the other is refused as
  `already_resolved` and leaves no audit record, and the first resolution's status, time and deletion date
  stand.
* **Room close** is the coarse content takedown: everybody in that room instance is disconnected (the
  usual "room closed") and its saved state is deleted. Closing by code with `--instance` is refused if the
  code now belongs to another instance (codes are reused).
* **Profile remove** clears the saved nickname, avatar and the Google display name (the default nickname)
  of an account. Statistics stay. The Google name comes back at the next sign-in; if that is the problem,
  restrict the account.
* **AI judge kill switch.** `judge disable` stops the AI judge: the button disappears with the next update
  the players receive and a new request is refused. What it guarantees, exactly: (1) **no new call** reaches the
  provider once the command returned; (2) a call **already in flight is cancelled** (its HTTP request is aborted),
  but the provider may already have received it, and what was sent cannot be recalled; (3) **no verdict is
  applied** after the command returned: a verdict that arrives after the switch went off is discarded (also if
  you enable it again meanwhile) and the room shows the usual "the AI is busy" failure. It lives only in memory: **a restart turns the judge on again**. To turn
  it off for good, remove the `cloudflare-*` keys from the Secret. It is for abuse of the model or an
  incident, not a budget tool (the daily Neuron budget is automatic).
* **Public games switch.** `public disable` turns public games off; `public enable` turns them on again.
  While they are off: nobody can create a public room (the app says public games are paused and suggests a
  private game); the list of public games is empty and says they are paused; nobody new can join a public
  room, by the list, by code or by link (they get the usual "You can't join this game right now"); no new
  automated opponent is added (the ones already seated stay and keep playing). Players already seated in a public room (also one who reconnects) finish
  their game. Private rooms are not affected at all. It takes effect when the command returns. It lives only
  in memory: **a restart goes back to the chart value** `server.publicRooms.enabled` (env
  `PUBLIC_ROOMS_ENABLED`, default `true`); set it to `false` to keep public games off across restarts and
  deploys. The dashboard stat *Public games switch* and `moderate status` show the state.

Every change is written to the audit log (`moderation_actions`: time, operator, action, target, report,
your note) in the same transaction as the change when it touches the database. A closure, a profile
removal and a judge or public-games switch are audited right after they happen; if that record cannot be stored the
command says `WARNING`. Read the log with SQL when you need it
(`SELECT at, operator, action, target_kind, target_id, details FROM moderation_actions ORDER BY at DESC`).

## Retention

Run by the server itself once a day and once at start (counts only are logged, never text).

| Data | Kept | Then |
|---|---|---|
| Open report | at most 90 days after it was filed | deleted, resolved or not |
| Resolved report | 30 days after you resolved it (never later than 90 days after filing) | deleted |
| Terms acceptance | 365 days (an older one no longer counts: the player is asked again) | deleted |
| Restriction | while in force, then 90 days after it expired or was lifted | deleted |
| Audit record of your actions | 365 days | deleted |
| Block | until the blocker unblocks | never purged by time |
| Room content | the room's own lifecycle (idle 30 min, janitor 6 h) | gone with the room |

`web/static/privacy.html` states the same figures in all three languages; change both together.

There are **no backups**: the shared Postgres has no dump or WAL archive and its volume no backup target, so
whatever the retention deletes, or an account deletion removes, is gone for good, also for the authorities.
Server logs (Fluent Bit to Loki) are kept **7 days** and hold **no player IP addresses**: Cloudflare Tunnel is in
front of the cluster and the servers only see internal addresses (Cloudflare handles the IPs as network provider).

## Appeals

A player who thinks a restriction or a decision is wrong writes to **aresiusxp@gmail.com**. The owner re-reads
the report and the audit log and decides **within 14 days**; `moderate unrestrict` lifts a restriction at once.
The server stores no email, so it cannot look anybody up: ask for the guest or account id the player can see (or
any detail that identifies the report), or lift by date. Reply with the decision, in the player's language when
you can.

## Urgent safety cases

A threat to someone's life (also self-harm that looks imminent), grooming (somebody steering a young player
towards sexual talk, private contact, photos or a meeting) and sexual content involving a minor are not normal
reports. `TuttiFruttiUrgentReport` pages you for the reasons `violence`, `self_harm`, `sexual` and
`child_safety` (shown to players as "A child at risk", for grooming or sexual content involving a minor); such a case
can also arrive as `harassment` or `other`, or by email. Act **within 24 hours of seeing it**, in this order:

1. **Read it** (`moderate reports show ID`) and decide whether it is real danger. If not, triage it as usual.
2. **Keep the evidence in the report only.** Do not copy it into files, screenshots, chats or mail of your own.
   The game holds no images; if a text points to images or a link elsewhere, never open, download or screenshot
   them. Leave the report **open** while the authorities may need it: an open report is kept up to 90 days
   after filing, a resolved one is deleted 30 days after you resolve it, and there are no backups.
3. **Stop the harm:** `moderate room close --operator you --instance ROOMINSTANCE` if the room still runs, then
   `moderate restrict --operator you --report ID [--user USERID] --reason "..."` (no `--days`: until you lift
   it).
4. **Report it:**

   | Situation | Whom to tell |
   |---|---|
   | Life at risk now, in the Netherlands | **112** |
   | Netherlands, no immediate danger | Police, **0900-8844** |
   | Child sexual abuse material, any country | **Meldpunt Kinderporno** ([meldpunt-kinderporno.nl](https://www.meldpunt-kinderporno.nl), run by Offlimits) |
   | Life at risk now, in Argentina | **911** |
   | Argentina, no immediate danger | **UFECI** (Unidad Fiscal Especializada en Ciberdelincuencia), denunciasufeci@mpf.gov.ar |
   | Country unknown | The **Dutch police** (0900-8844; 112 if a life is at risk now) |

   Give them the report id, the time it was filed, the room instance and match instance ids, the guest and
   account ids and the text as the report shows it, typed or pasted into their own form or mail (that is not
   a copy you keep). Say that the server never sees player IP addresses.
5. **Resolve** the report once the authorities no longer need it, with a note of whom you told and when
   (`--note "police 0900-8844, 2026-11-02, ref ..."`).

## Notices of illegal content by email

Anybody may write to **aresiusxp@gmail.com** that something in the game is illegal or breaks the terms.

1. **Acknowledge within 3 days** (a short reply in their language). Ask for what you need to find the content
   if it is missing: room code, date and time, nickname, the text.
2. **Decide within 7 days.** Find the matching reports (`moderate reports list --status all`) and act like for a
   report: `room close`, `profile remove`, `restrict`. A notice about child sexual abuse material or a threat
   follows "Urgent safety cases" and its 24 hours instead.
3. **Reply with the outcome** (what you removed or restricted, or why nothing) and the appeal route: answer to the
   same address; appeals are decided within 14 days.

The mail thread is the record of the notice; keep no other copy.

## Removal orders (ATKM)

The Dutch **ATKM** (Autoriteit online Terroristisch en Kinderpornografisch Materiaal) can order a hosting
service to remove **terrorist content within 1 hour** (EU Regulation 2021/784) and to make **child sexual abuse
material inaccessible within at most 12 hours** (Wet bestuursrechtelijke aanpak online kinderpornografisch
materiaal). Orders arrive in writing at the contact address, so keep **mail notifications for
aresiusxp@gmail.com on the phone**, next to ntfy, with sound allowed.

On an order: `room close` the room, `profile remove` the account, `restrict` the identity, and confirm to the
ATKM the way the order asks. Both laws expect the removed data to be preserved for the authorities (the
terrorist-content regulation: six months), while the reports table keeps at most 90 days and nothing is backed
up: ask the ATKM in your confirmation how they want it preserved.

## Absence

Before being away for **more than 2 days**:

1. Run the daily check and decide every open report.
2. If you can still read ntfy and mail where you go, nothing else changes: the alerts keep working and the
   commitments stay (urgent cases within 24 hours, reports within 7 days).
3. If you will be unreachable, switch off what nobody can watch:

   ```sh
   moderate public disable --operator you   # no new public rooms or players, empty list, no new bots
   moderate judge disable --operator you    # the AI judge
   ```

   Players already seated in a public game finish it; private rooms (friends with a code) keep working. Both
   switches live in memory and **a restart turns them back on** (any deploy or node reboot restarts the
   server). To make public games stay off across restarts, also set the chart value
   `server.publicRooms.enabled: false` (env `PUBLIC_ROOMS_ENABLED=false`) in the app's values in
   `AresiusXP/argocd-apps`; to keep the judge off, remove the `cloudflare-*` keys from the Secret. Reports
   (from private rooms, and about content already seen) keep arriving and wait for you; an absence longer than
   7 days breaks the 7-day commitment.
4. Back home: `moderate status`, triage the oldest first, then `moderate public enable --operator you` and
   `moderate judge enable --operator you` (and set `server.publicRooms.enabled` back to `true` if you changed it).

## Limits

* Identities are **guest cookies** (and Google ids). A determined person gets a new one by clearing
  cookies. Restrictions and blocks slow down, they do not stop.
* **Only the urgent reasons page you.** Every other report waits for the daily check or the 20-hour reminder;
  the AI verdict is advisory, and a room is only closed when you close it.
* Evidence only exists while the room does (or while the reporter saw it): a report about something
  nobody saw is `UNVERIFIED`.
* No player IP addresses and no backups (see "Retention").
* The tool is single-operator: the kill switch and any command are available to anyone who can exec into
  the pod; the audit log records the name they typed.

## Open points

* **Preservation after a removal order** is longer (six months for terrorist content) than the report retention
  (90 days at most), and there are no backups.
