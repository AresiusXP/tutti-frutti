# Moderation runbook

How the owner of Tutti Frutti handles reports, restrictions and takedowns. The design is in
[`DESIGN.md`](DESIGN.md) section 9c ("Content safety"); this is the daily manual. It makes **no promise
about response times** and gives no legal advice (see "Serious concerns" and "Limits").

Players can report a player (nickname/avatar), a custom category label, an answer or an AI verdict, and
can block another player. There is **no public admin page**: moderation is a command run by the owner
inside the server's pod.

## The daily check

Alertmanager is disabled in the homelab, so **nothing notifies you**. The metrics
`tuttifrutti_reports_open` and `tuttifrutti_report_oldest_open_age_seconds` (dashboard, row *Accounts*:
*Open reports*, *Oldest open report*) show the backlog, but a metric is not a notification process. The
routine is this, once a day:

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
```

If `open reports` is 0 you are done. Otherwise triage them (next section). If the command cannot reach
the control socket it says so (the server is down, or the socket could not be created: see the server
log, `moderation control socket`): fix that first, because nobody can be moderated meanwhile.

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
| `status` | open reports, age of the oldest, restrictions in force, number of blocks (a count, never who blocked whom), AI judge state |
| `reports list [--status open\|resolved\|all] [--limit N]` | summaries, oldest first for `open`, newest first otherwise |
| `reports show ID` | the whole report, see above |
| `reports resolve --operator NAME --note TEXT [--dismiss] ID` | closes a report; audited |
| `restrict --operator NAME (--guest ID \| --report ID) [--user ID] --reason TEXT [--days N]` | see "Restrictions" |
| `unrestrict --operator NAME ID` | lifts a restriction (the id is printed by `restrict`) |
| `room close --operator NAME [--instance ID] [CODE]` | closes exactly that room instance |
| `profile remove --operator NAME --user ID` | deletes the saved profile text of an account |
| `judge status` / `judge enable\|disable --operator NAME` | the AI judge's kill switch |

Examples:

```sh
moderate restrict --operator ana --report 5c1d… --user 3f9a… --reason "slurs in custom labels, 3rd report" --days 30
moderate restrict --operator ana --guest 8Qm… --reason "spam nicknames"          # until you lift it
moderate unrestrict --operator ana 7e02…
moderate room close --operator ana KT7P
moderate room close --operator ana --instance 2c9b…     # closes that instance only, wherever it is
moderate room close --operator ana --instance 2c9b… KT7P  # the room holding KT7P must be that instance
moderate judge disable --operator ana
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

Every change is written to the audit log (`moderation_actions`: time, operator, action, target, report,
your note) in the same transaction as the change when it touches the database. A closure, a profile
removal and a judge switch are audited right after they happen; if that record cannot be stored the
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

## Appeals

A player who thinks a restriction is wrong writes to **aresiusxp@gmail.com** (the address on the terms and
privacy pages). The owner re-reads the report and the audit log and decides; `moderate unrestrict` lifts
it at once. The server stores no email, so it cannot look anybody up: ask for the guest or account id
the player can see (or any detail that identifies the report), or lift by date. Answer in the player's
language when you can.

## Serious concerns

Illegal content, threats to a person's life or safety, or anything involving a child's safety is not a
normal report. Do **not** just resolve it and move on:

1. Take the evidence now: `moderate reports show ID` (and `--json`) into a file you keep, before the 90-day
   retention deletes it. Note the room instance and match instance ids.
2. Close the room (`room close`) and restrict the identity if that stops harm.
3. Decide, as the owner, whom to tell: the local authorities, the platform (Google Play's developer
   support) or both, as appropriate where you and the people involved live. This document gives no
   legal advice and the repository claims no legal compliance; that call is yours.

## Limits

* Identities are **guest cookies** (and Google ids). A determined person gets a new one by clearing
  cookies. Restrictions and blocks slow down, they do not stop.
* **No real-time guarantee.** Nobody is paged. A report waits until your daily check, the AI verdict is
  advisory, and a room is only closed when you close it.
* Evidence only exists while the room does (or while the reporter saw it): a report about something
  nobody saw is `UNVERIFIED`.
* The tool is single-operator: the kill switch and any command are available to anyone who can exec into
  the pod; the audit log records the name they typed.
