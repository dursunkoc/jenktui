# Development plan: v0.2 and open TODO items

Written 2026-11-12 from [TODO.md](../TODO.md). It splits every open item into issues that separate agents can take in parallel without colliding. Each issue is a standalone brief: an agent given one issue ID and this file should have what it needs.

## How to use this plan

- **Waves** run in order. Issues inside a wave run in parallel, except those marked *serial*. A wave starts when the issues it depends on are merged, not when the whole previous wave is.
- **Lanes** are a file-ownership rule: one agent owns a lane's files for the whole wave. Two issues in the same lane are done by the same agent, one after the other.
- **Docs travel with the code:** as CLAUDE.md requires, each issue updates README, GLOSSARY and `docs/ui-contract.md` on its own branch, in the same change as the behaviour. The "Docs" line in each issue says what to touch.
- **Integrator:** one agent (or the owner) per wave merges the branches and resolves conflicts (mostly README's keys table). It alone owns the wiki, the screenshots, HANDOFF and the TODO.md ticks.
- **Owner-only** issues need a person with a real machine or a real Jenkins; agents do not take them.

## Rules for every issue

Read these before starting any issue.

1. Read [docs/HANDOFF.md](HANDOFF.md) and [docs/ui-contract.md](ui-contract.md) first, then the files named in the issue.
2. Work in your own git worktree on a branch named `v0.2/<ID>-<slug>` (for example `v0.2/W1-P1-sysint`). Never push, tag or release.
3. Commit with the repo-local identity (`dursunkoc <dursunkoc@gmail.com>`), never the global one. Check with `git config user.email`.
4. Before every commit: `export PATH=/opt/homebrew/bin:$PATH && gofmt -l . && go vet ./... && go test -count=1 ./...`, all clean.
5. Bubble Tea rules: all async work is a `tea.Cmd`, no goroutines of our own; stale results are dropped with the existing `gen`/`epoch`/`logGen` counters; nothing sleeps in tests; anything time-based takes the injected clock (`Deps.Now`).
6. Colours only from `core.Theme`. Keys: reuse `core.Key*`, and take new keys only from the [key allocation](#key-allocation) table.
7. Screens never import each other. A screen that needs another screen gets a factory through its `Deps`, wired in `main.go`.
8. Never store credentials. Any new Jenkins field or endpoint must be named in the `tree=` query and added to the fake (`internal/jenkins/jenkinstest`), and is listed for a real-Jenkins check (see O3).
9. Do not edit the [shared files](#shared-files) unless your issue owns them. README, GLOSSARY and `ui-contract.md` are the exception: every issue updates them for its own behaviour, and the integrator resolves the merge conflicts.
10. Tests: unit tests against `jenkinstest` or the package's fakes; add an `e2e_test.go` scenario when the change spans screens.

## Shared files

Only the named owner edits these in a wave.

| File | Owner |
|---|---|
| `internal/ui/monitor/monitor.go` (`Deps`, Update, `key`, `Keys`, `FullHelp`) | W1 **M0**, then lane **MA** (monitor actions) |
| `internal/ui/monitor/render.go` | lane **MD** (monitor display) |
| `internal/ui/monitor/logview.go` (including `logKeys()`, moved there by M0) | lane **LV** (log view) |
| `internal/ui/monitor/monitor_test.go` `fakeClient` | whoever adds methods to `monitor.Client`; prefer optional interfaces (below) |
| `internal/jenkins/jenkinstest/server.go` | W0 issue **F2** only; later issues ask the integrator or add small handlers after F2 has merged |
| `internal/jenkins/status.go` | lane **JC** (client) |
| `internal/ui/core/*`, `docs/ui-contract.md` | W0 **F1**, then W3 lane **CORE** |
| `main.go` | W1 **M0**, W2 lane **MA**, W3 lane **CORE** |
| README, GLOSSARY, `docs/ui-contract.md` | every issue, for its own behaviour; the integrator resolves conflicts |
| HANDOFF, TODO.md, wiki (`../jenktui.wiki`), `docs/images` | integrator |

**Adding a capability to the monitor:** the monitor tests use a hand-written `fakeClient` (`monitor_test.go`) that implements `monitor.Client`. Adding a method to that interface breaks it. Instead, declare a small optional interface next to the feature (`type stopper interface { StopBuild(...) error }`) and type-assert `m.deps.Client` to it; the feature is hidden when the client does not implement it. `*jenkins.Client` implements all of them.

## Key allocation

Keys already bound (from `grep -rn 'WithKeys(' internal/ui`). A row marked "new" is reserved for the issue named.

| Screen | Taken | New in v0.2 |
|---|---|---|
| Run Monitor list | `↑↓ k j` `enter` `s` `R` `esc` `q` `?` | `o` open in browser (MA1) · `y` copy build URL (MA1) · `r` rerun (CORE1) · `X` abort (MA4) · `f` next failed row (MA2) |
| Build details | `↑↓` `l` `L` `enter` `esc` `q` | `o` `y` (MA1) · `t` test report (MA3) · `X` abort (MA4) · `r` rerun (CORE1) |
| Console log | `↑↓` `pgup pgdn ctrl+f ctrl+b space` `g home` `G end` `/` `n` `N` `:` `esc` `q` | `e`/`E` next/previous error (LV1) · `w` wrap (LV2) · `S` save to file (LV3) · `y` copy current line (LV4) · `o` open build in browser (LV4) |
| Opened Collection | list keys (`/ s space a n shift+↑↓`) `t` `m` `e` `x` `R` | `o` open job in browser (UI1) |
| Job Catalog | list keys, `b` `B` `r` | `o` open job in browser (UI1) |
| Trigger summary (several jobs) | `y n esc ↑↓ k j` | `e` edit job (G2) · `pgup pgdn` scroll |
| Any screen | `ctrl+c` | `ctrl+p` jump to (CORE4), handled by the Router |

`y` is also "yes" inside the Collections delete dialog; that dialog takes every key while open, so there is no clash.

## Overview

```
W0  F1 core statuses ─┐        F2 fake Jenkins v2 ──────────────────┐
                      │                                             │
W1  T* housekeeping   │  P1 sysint   JC1 build context  JC2 test report  JC3 stop/cancel
    G* known gaps     │      │           │                  │               │
                      ▼      ▼           ▼                  ▼               ▼
    M0 monitor seams ─────────────────────────────────────────────┐
W2              MD1→MD2→MD3        LV1→LV2→LV3→LV4      MA1→MA2→MA3→MA4→MA5     UI1
                (render.go)        (logview.go)         (monitor.go)           (catalog, collections)
W3  CORE1 rerun → CORE2 open a Collection from the CLI → CORE3 failures overview → CORE4 ctrl+p → CORE5 cleanup
W4  integrator: docs, wiki, screenshots, TODO; owner: real-Jenkins check and release
```

Agents at peak: W1 runs up to 9 in parallel (P1, JC1–3, T1–T5 split as the owner likes, G*), then M0 alone; W2 runs 4 (MD, LV, MA, UI1).

---

## Wave 0: foundations

### F1: Unstable and Aborted in `core.Status`
- **From:** Tech debt ("`core.Status` has no unstable or aborted values").
- **Owns:** `internal/ui/core/theme.go`, `docs/ui-contract.md` (Theme section), and the minimal change in `internal/ui/monitor/render.go` (`badge`) and `internal/ui/catalog/catalog.go` to use the new values.
- **Depends on:** nothing.
- **Do:** add `StatusUnstable` (Warn colour, `UNSTABLE`) and `StatusAborted` (Muted, `ABORTED`) to `core.Status` and `Theme.Badge`. Replace the monitor's own labels for them with `th.Badge`. Map Jenkins colours `yellow*` and `aborted*` to them in `jenkins.StatusFromColor` if it does not already.
- **Tests:** `core_test.go` covers both badges; the monitor and catalog tests still pass with the same visible text.
- **Docs note:** ui-contract Theme list (edited here, since F1 owns it); GLOSSARY if it lists statuses.

### F2: Fake Jenkins v2 *(serial, one agent)*
- **From:** Tech debt ("the fake ignores `tree=`") and the fake support every v0.2 client feature needs.
- **Owns:** `internal/jenkins/jenkinstest/server.go`, `server_test.go`.
- **Depends on:** nothing. Blocks JC1–JC3 and every monitor feature that needs new data.
- **Read first:** `server.go` (`serve`, `buildJSON`, `runJSON`, `serveQueue`), `internal/jenkins/status.go`, `trigger.go`.
- **Do, in this order, one commit each:**
  1. **Build history.** Today a job holds one build (`node.last`). Keep `LastBuild(b)` working and add `Builds(bs ...Build)` so a job holds several; `/job/x/<n>/api/json` serves any of them, and `/job/x/api/json?tree=builds[...]{0,N}` lists them newest first.
  2. **Build fields:** `Build` gains `Causes` (short description and user), `Changes` (commit id, author, message), `EstimatedDuration`, and `Params` (name and value). Serve them as Jenkins does: `actions[causes[shortDescription,userId]]`, `changeSets[items[commitId,author[fullName],msg]]`, `estimatedDuration`, `actions[parameters[name,value]]`. Note that `actions` is a list with mixed entries (and nulls), as on real Jenkins.
  3. **Test report:** `Job.TestReport(r)` serves `/job/x/<n>/testReport/api/json` (`failCount`, `passCount`, `skipCount`, `suites[cases[className,name,status,errorDetails,errorStackTrace]]`); 404 when there is none.
  4. **Stop and cancel:** `POST /job/x/<n>/stop` marks the build `ABORTED` and not building; `POST /queue/cancelItem?id=N` removes the item. Both require the crumb when crumbs are enabled and record the call (like `Triggers()`), so tests can assert them.
  5. **Honour `tree=`** for the endpoints the client uses: return only the named fields (a small recursive filter over the response map, supporting `a,b[c,d]` and the `{0,N}` range). Run the whole test suite: any client query that names too few fields now fails, which is the point. Fix the client query in the same commit if so, and list each fix in the commit message.
- **Tests:** `server_test.go` covers each new endpoint and the `tree=` filter (nested lists, ranges, unknown fields).
- **Docs note:** HANDOFF "Jenkins facts" (the fake now honours `tree=`), README Development paragraph (it says the fake ignores `tree=`).

---

## Wave 1: parallel, by package

### P1: `internal/sysint`: open, copy, notify
- **From:** v0.2 §1 (open, copy) and §3 (notifications), platform part only.
- **Owns:** a new package `internal/sysint` (name it so; it must not import any UI package).
- **Depends on:** nothing.
- **Do:** three functions behind one struct with an injectable runner, so tests never run real commands:
  ```go
  type Runner func(name string, args ...string) error   // exec.Command(...).Run in production
  type Sys struct { Run Runner; GOOS string; Stdout io.Writer }
  func (s Sys) Open(url string) error      // open (darwin), xdg-open (linux), rundll32 url.dll,FileProtocolHandler (windows)
  func (s Sys) Copy(text string) error     // OSC 52 escape to Stdout first (works over SSH and tmux), then pbcopy / wl-copy / xclip / clip.exe as fallback
  func (s Sys) Notify(title, body string) error // osascript -e 'display notification …' / notify-send / no-op on windows
  func (s Sys) Bell()                      // writes "\a" to Stdout
  ```
  Only allow `http`/`https` URLs in `Open`. Quote `osascript` arguments safely (no string concatenation into AppleScript; pass them as `argv` with `on run argv`).
- **Tests:** table tests with a recording `Runner` for each GOOS; `Open` refuses `file:` and `javascript:` URLs; the OSC 52 sequence is base64 of the text.
- **Docs note:** none until the UI uses it.

### JC1: build context in the client
- **From:** v0.2 §5 (changes, causes, estimated duration) and §4 (parameters for rerun).
- **Owns:** `internal/jenkins/status.go`, `status_test.go` (new) or `client_test.go`.
- **Depends on:** F2 steps 1–2.
- **Do:** extend `jenkins.Build` with `EstimatedDuration time.Duration`, `Causes []Cause{Text, User}`, `Changes []Change{ID, Author, Message}`, `Params map[string]string`, and name the fields in `BuildStatus`'s `tree=`. Add `BuildHistory(ctx, fullName, n int) ([]Build, error)` returning the last `n` builds, newest first, with only number, result and building (cheap: one request, `builds[number,result,building]{0,n}`).
- **Tests:** against the F2 fake, including a build with no causes or changes, a running build (`result` null), and `actions` containing nulls.
- **Real-Jenkins check:** yes (O3).

### JC2: test report client
- **From:** v0.2 §2 (test report).
- **Owns:** new file `internal/jenkins/testreport.go` and its test.
- **Depends on:** F2 step 3.
- **Do:** `TestReport(ctx, fullName, number) (TestReport, error)` with counts and the failed cases (class, name, error details, stack trace, capped at 200 cases and 200 lines of stack per case). 404 returns `ErrNoTestReport`.
- **Real-Jenkins check:** yes (O3); Maven, Gradle and JUnit-plugin reports may differ.

### JC3: stop and cancel client
- **From:** v0.2 §4 (abort).
- **Owns:** new file `internal/jenkins/control.go` and its test. Reuse `crumb()` from `trigger.go` without editing that file, unless a small export is unavoidable.
- **Depends on:** F2 step 4.
- **Do:** `StopBuild(ctx, fullName, number) error` and `CancelQueue(ctx, id int) error`, with the crumb, retrying once with a refreshed crumb on 403 as `Trigger` does. Report 403 as `ErrForbidden` with Jenkins' text.
- **Real-Jenkins check:** yes (O3); some servers answer `stop` with a 302.

### M0: monitor seams *(one agent; must merge before wave 2 starts)*
- **Why:** wave 2 runs three agents inside `internal/ui/monitor`. Their features cross file lines (a notice drawn by `render.go` for a key handled in `monitor.go`, log keys listed by `Keys()` in `monitor.go`). M0 cuts those seams up front, with no visible change, so each lane then edits only its own files.
- **Owns:** `internal/ui/monitor/*.go`, `internal/config/config.go`, `main.go`.
- **Depends on:** F1 (merged), P1 (for the `Sys` interface; or declare the interface in `monitor` and let P1 satisfy it).
- **Do, with no change in behaviour:**
  1. **`Deps` fields for all of wave 2 and CORE1:** `Sys` (interface: `Open`, `Copy`, `Notify`, `Bell`; nil means the keys are hidden), `SaveDir string` (LV3), `Notify string` (MA5: `failures`, `all`, `off`), `Rerun func(job string, params map[string]string) core.Screen` (CORE1; nil hides `r`).
  2. **Config:** `notify` in `config.toml` (`Config.Notify`, validated, default `failures`).
  3. **Wiring in `main.go`:** pass `sysint.Sys{...}` and `cfg.Notify` into `monitor.Deps`.
  4. **Log keys:** move the log-mode branch of `Keys()` into `func (m *model) logKeys() []key.Binding` in `logview.go`, and the log-mode key dispatch into `logview.go` too if it is not already there (`logKey`).
  5. **Render hooks in `render.go`:** a timed notice for `Subtitle()` (`m.notice`, `m.noticeUntil`, set by any lane with `m.flash(text)`); a details-header slot (`detailHeader() []string` that joins sections registered by lanes, each a `func(runState) []string`); a confirmation prompt slot (`m.confirm *confirmPrompt{text string; yes tea.Cmd}`) drawn above the help bar and handled by one `case` in `key`.
  6. **Manual refresh flag:** `startCycle(manual bool)` records `m.manual` for MD1.
- **Tests:** the existing suite passes unchanged; small tests for `flash` expiry with the injected clock and for the confirm prompt (`y` runs the command, `n`/`esc` cancels).
- **After M0:** lane MD owns `render.go` and new display files, lane LV owns `logview.go`, lane MA owns `monitor.go` and `main.go` for wave 2. A lane that still needs a line in another lane's file asks that lane's agent.

### T1–T5: housekeeping *(each independent; one agent each or one agent for all)*
- **T1, catalog status copy:** delete the catalog's `statusFromColor` (`internal/ui/catalog/catalog.go:266`) and use `jenkins.StatusFromColor` mapped to `core.Status`. Owns `internal/ui/catalog/catalog.go`. After F1.
- **T2, list filter box:** replace the hand-written filter box in `internal/ui/widgets/list.go` with `bubbles/textinput`, keeping every current behaviour (spaces typed, `↓` and `Enter` move to the list, `Esc` clears, `↑` on the first row returns). Owns `internal/ui/widgets`. `list_test.go` must pass unchanged.
- **T3, slow trigger tests:** find why the trigger tests wait out cursor-blink commands (about 8s) and drop the blink commands in the test harness. Owns `internal/ui/trigger/*_test.go` and `internal/ui/params` if needed.
- **T4, Loading tick test:** make the replayed-first-tick case testable in `core.Loading` (for example by keeping the spinner's tag in `Loading` and comparing it). Owns `internal/ui/core/loading*.go`. After F1 (same package).
- **T5, ADR for the stack choice:** write `docs/adr/0001-go-bubble-tea.md` (context, options: Go with Bubble Tea and Python with Textual, decision, consequences). Ask the owner for the original reasons if HANDOFF does not give enough; do not invent them.

### G1–G5: known gaps *(each independent)*
- **G1, Multibranch `parent_job`:** when `parent_job` is itself a Multibranch Project, list its branches as branches (project › branch) and apply the Branch Filter. Owns `internal/jenkins/client.go` (`ListCatalog`) and `internal/ui/catalog`. After F2 (fake routes) and after T1 (same catalog file). Real-Jenkins check: yes.
- **G2, Common Parameter override for jobs without parameters of their own:** today the per-job step is skipped, so such a job cannot override a Common Parameter. Show the per-job step when a job has Common Parameters, even if none of its own. Owns `internal/ui/trigger`, `internal/paramsmerge`. After T3 (same tests).
- **G3, blinking cursor in parameter forms:** use the `textinput` cursor blink. Owns `internal/ui/params`. Coordinate with T3, which strips blink commands in tests.
- **G4, catalog count with the Branch Filter placeholder:** "N of M jobs" must not count the placeholder row. Owns `internal/ui/catalog`. Do with T1 (same agent).
- **G5, Windows install snippet goes stale:** make the PowerShell snippet in README and the wiki look up the latest release (`Invoke-RestMethod https://api.github.com/repos/dursunkoc/jenktui/releases/latest`) instead of hardcoding `$v`. Docs only; hand the text to the integrator. Untested until O1.

---

## Wave 2: the monitor and the log view

Three lanes in `internal/ui/monitor`, one agent each, plus UI1. All start after M0 has merged. Each lane's issues are serial inside the lane. Where a lane still needs a line in another lane's file, it asks that lane's agent rather than editing it.

### Lane MD: monitor display (owns `render.go`)

**MD1: spinner only on manual refresh**
- **From:** v0.2 §8 and Known gaps (spinner flashes every 3 seconds).
- **Do:** show the header spinner for the first load and for `R` only, not for automatic polls, using the `m.manual` flag from M0.
- **Tests:** an automatic tick leaves no spinner in the header; `R` shows it.

**MD2: progress and age in the list** *(after JC1)*
- **From:** v0.2 §5 (ETA, started ago).
- **Do:** running rows show `1m36s / ~4m` (from `EstimatedDuration`; nothing when it is unknown or the build overruns, then `4m12s (over)`), and every finished row shows how long ago it started (`2m ago`, `3h ago`, `yesterday`) using `Deps.Now`.
- **Tests:** with the injected clock: estimate known, unknown, overrun; ages at minute, hour and day boundaries.

**MD3: history dots and build context in the details view** *(after JC1)*
- **From:** v0.2 §5 (history, who started it, commits).
- **Do:** the list row ends with the last five results as dots (`●●●○●`, theme colours, newest on the right), fetched with `BuildHistory` once per finished build (not every poll). The details view gets a header section (registered in M0's details-header slot): the causes ("Started by alice", "Branch indexing") and up to five commits (`abc1234 author: message`, cut to the width).
- **Fetching:** the fetch is a `tea.Cmd` with a `historyMsg`; the `Update` case for it belongs to lane MA's file, so lane MD writes the command and the message type in a new file `history.go` and asks MA to add the three-line `case`. Use an optional interface (`historian`) so `fakeClient` keeps compiling.
- **Tests:** dots for success, failure, unstable, running; a build with no changes shows no commit block.

### Lane LV: log view (owns `logview.go`)

LV issues add their keys to `logKeys()` (moved into `logview.go` by M0) and their key handling to `logKey`.

**LV1: error lines** — highlight lines matching a built-in list (`ERROR`, `FAILED`, `FAILURE`, `Exception`, `Caused by:`, `BUILD FAILURE`, `npm ERR!`, `error:`) in the Fail colour, and `e`/`E` jump to the next and previous one (the footer shows `error 3/7`). The list is a package variable, not configurable yet. Tests: matching, jumping with wraparound, nothing found.

**LV2: wrap** — `w` toggles wrapping long lines (Known gaps: no horizontal scroll). Line numbers stay on the first visual row of a wrapped line; scrolling, find and go-to-line keep working on log lines, not visual rows. Tests: a wrapped long line, go to line and find while wrapped.

**LV3: save the log** — `S` writes the whole log (fetched again in full from `consoleText` offset 0, not the 10,000-line buffer) to `./<job>-<n>.log` in the current directory, with `/` in the job name replaced by `_`. Never overwrite: add `-1`, `-2`. The footer shows the path or the error. The write is a `tea.Cmd`; the directory comes from `Deps.SaveDir` (added by M0; empty means the working directory) so tests use `t.TempDir()`. For a stage log, save what is shown. From TODO "Download logs to local upon request".

**LV4: copy and open from the log** *(after P1)* — `y` copies the current line (or the current match) and `o` opens the build in the browser, through `Deps.Sys` (added by M0).

### Lane MA: monitor actions (owns `monitor.go`)

**MA1: open and copy** *(after P1)*
- **Do:** `o` opens the highlighted build (or the job, if it has no build) and `y` copies its URL, in the list and the details view, through `Deps.Sys` (M0). Confirm with `m.flash("copied")`. Hide both keys when `Sys` is nil.
- **Tests:** a recording fake `Sys`; no build yet opens the job URL; a missing job does nothing.

**MA2: straight to the failure**
- **From:** v0.2 §2.
- **Do:** `Enter` on a failed build opens the details with the marker on the first failed stage (the deepest failed step if the steps are loaded), and `l` then opens that log scrolled to the end, not following. `f` in the list moves the cursor to the next failed row.
- **Tests:** a pipeline with a failed second stage; a failed freestyle job (no stages) opens the full log at the end.

**MA3: test report in the details view** *(after JC2)*
- **Do:** for a finished build, fetch the test report once (optional interface `reporter`); a section in M0's details-header slot shows `3 failed · 812 passed · 4 skipped` and `t` opens a modal listing the failed tests, with `Enter` showing a test's stack trace (reuse the log view's rendering for it if it fits without editing `logview.go`; otherwise ask lane LV for a small constructor). No report: show nothing.
- **Tests:** report with failures, all passed, 404.

**MA4: abort** *(after JC3)*
- **From:** v0.2 §4. **Reverses a settled decision** ("abort and rerun are out of v1"); the integrator updates HANDOFF's settled decisions.
- **Do:** `X` on a running build or a queued item asks "Abort api/deploy #57? y/n", (M0's confirm prompt), then calls `StopBuild` or `CancelQueue` (optional interface `stopper`) and polls the row at once. Errors are shown with `m.flash`.
- **Tests:** abort running, cancel queued, refused (403), cancelled at the prompt.

**MA5: notify when a build finishes** *(after P1)*
- **From:** v0.2 §3.
- **Limit (state it in the docs):** only the top screen receives messages, and a monitor that is left bumps `gen` and stops polling. So notifications fire only while that monitor is the screen on show (which is the common case: jenktui open in a tmux pane or a background window). Notifying from a monitor that is not on show needs a Router change and is out of scope here; record it in TODO.
- **Do:** when a row moves from running or queued to finished, and the monitor saw it running (not on its first load), call `Bell()` and `Notify("jenktui", "FAILED api/deploy #57")`. The mode comes from `Deps.Notify` (M0 added `notify = "failures" | "all" | "off"` to `config.toml` and wired it).
- **Tests:** transitions with the injected clock: first load does not notify, success with `failures` does not, failure does, `off` never.

### UI1: open from the catalog and a Collection *(after P1; owns `internal/ui/catalog`, `internal/ui/collections`; after T1 and G4)*
- `o` opens the highlighted job in the browser in the Job Catalog and an opened Collection. The job URL comes from the job list (`url` is already in the job JSON on real Jenkins; add it to `ListCatalog`'s `tree=` if missing, in `client.go`, which G1 owns: do it after G1 or ask that agent). Add a `Sys` field to each screen's `Deps` or options; lane MA, which owns `main.go` in wave 2, adds the wiring line.

---

## Wave 3: cross-screen work (one agent, lane CORE, serial)

Owns `main.go`, `internal/ui/core`, `docs/ui-contract.md`, `internal/ui/entry`, and the new packages below.

**CORE1: rerun** *(after JC1 and all of lane MA, which owns `monitor.go` until then)*
- **From:** v0.2 §4. **Reverses a settled decision**, as MA4.
- **Do:** `r` in the Run Monitor list and details reruns the highlighted build with its parameters. The monitor cannot open the trigger screen itself (screens never import each other), so it calls `Deps.Rerun` (the field M0 added), which this issue wires in `main.go` to `trigger.New` with the values prefilled (`params.Options{Prefill}` already exists). Password parameters cannot be read back, so the form asks for them; a job without parameters gets the plain confirmation. The new run joins the same monitor through the trigger's `Next`, as today. The key handling is a small edit in `monitor.go`; do it after lane MA is finished.
- **Tests:** an e2e scenario: trigger with parameters, open the monitor, `r`, the form is prefilled, confirm, a new build is tracked.

**CORE2: open a Collection from the command line**
- **Do:** `jenktui [collection]`: after login, go straight to that Collection's Run Monitor (case-insensitive name; unknown name: show the menu with a notice). Update `flag.Usage`. The Session gains a `StartCollection string`, read by the menu's first `Init`.
- **Tests:** e2e: start with a name, log in, land on the monitor; unknown name shows the notice.

**CORE3: failures overview on the menu**
- **From:** v0.2 §6 (moved from Later).
- **Do:** a new screen package `internal/ui/overview` and a menu item "Overview": every Collection with counts of failed, running and queued latest builds, `Enter` opens that Collection's monitor. It does its own polling (it cannot read the per-Collection monitors, which stop when left), at the slow interval, with a generation counter. Reuse `BuildStatus` with `lastBuild`. Decide with the owner whether it becomes the landing screen; default: a menu item.
- **Tests:** unit tests with a fake client; e2e: the menu, the overview, `Enter` lands on the right monitor.

**CORE4: jump with `Ctrl+P`**
- **From:** v0.2 §6.
- **Do:** the Router catches `ctrl+p` on every screen (it is not typeable text, so no text field needs it). Do not use `FullHelp() == nil` as a signal: it is also nil during loading, on errors, in the monitor's details and log views, and for every screen without `FullHelper`. If some widget turns out to bind `ctrl+p`, add an explicit optional `TextFocused() bool` interface instead. The Router pushes a palette screen from a factory in `Session` or `NewRouter`, so `core` does not import screens. The palette lists Collections and catalog jobs with the list widget's fuzzy filter; `Enter` on a Collection opens it, on a job opens the catalog filtered to it. Update `docs/ui-contract.md` (Router and keymap sections).
- **Tests:** core tests for the Router intercept; e2e: `ctrl+p`, type, `Enter`.

**CORE5: cleanup in `main.go`**
- Delete a Collection's monitor when the Collection is deleted (Known gaps), through a `Deleted` hook beside `Renamed` in `collections.Deps`.

**CORE6: colours** *(small; may run in W1 if F1 is merged)*
- **From:** v0.2 §8.
- **Do first:** check what already works. `lipgloss.AdaptiveColor` already switches between light and dark, and `termenv` (in `go.mod`) honours `NO_COLOR`; confirm both by running the app with `NO_COLOR=1` and on a light terminal profile, and record the result. Then fix only what is wrong (likely: the reverse-video cursor and the log highlight under `NO_COLOR`, which must stay visible without colour). A `theme = "light" | "dark" | "auto"` setting only if auto-detection proves unreliable.

---

## Wave 4: integration and release

**I1: integration (integrator)**
- Check README, GLOSSARY and `ui-contract.md` read as one document after the merges (each issue edited its own part): the Keys table matches the key allocation table, Scope says abort and rerun are now in, the Features list covers the new work.
- HANDOFF: settled decisions (abort and rerun now in, notifications only from the monitor on show), Jenkins facts (test report, stop and cancel, `actions` parsing, the fake honours `tree=`), Layout (`internal/sysint`, `internal/ui/overview`).
- Wiki pages: Keyboard Reference, Collections and the Run Monitor, Configuration, Troubleshooting (notifications on Linux need `notify-send`; copying over SSH needs a terminal with OSC 52).
- Screenshots: extend `screenshots_test.go`'s "shop" fixture (history, causes, a test report) and regenerate `docs/images`.
- TODO.md: tick what landed, move anything not done, and add "notify from a monitor not on show" to Later.

**O1–O4: owner-only**
- **O1:** run the Windows PowerShell install steps (with the G5 change) on a Windows machine.
- **O2:** `brew install dursunkoc/tap/jenktui` on macOS and, if possible, Linux.
- **O3:** real-Jenkins check of everything new: the original real-Jenkins checklist in TODO.md, plus JC1 fields (causes, changes, estimates, parameters, history), JC2 test reports from your build tools, JC3 stop and cancel, G1 Multibranch parent, MA5 notifications on your desktop, P1 copy over SSH and in tmux.
- **O4 (decision):** reading the API token from an environment variable or a command (`token_command`). It touches "credentials are never stored". No agent starts it until the owner decides; if yes, it becomes a W3 issue owned by lane CORE (`internal/ui/entry/login.go`, `internal/config`).
- Then release as in HANDOFF ("Releasing"), as `v0.2.0`.

## Issue index

| ID | Wave | Lane / owns | Depends on | Real Jenkins |
|---|---|---|---|---|
| F1 | 0 | core theme | – | |
| F2 | 0 | jenkinstest | – | |
| P1 | 1 | new `internal/sysint` | – | O3 (copy, notify) |
| JC1 | 1 | jenkins `status.go` | F2 | yes |
| JC2 | 1 | jenkins `testreport.go` | F2 | yes |
| JC3 | 1 | jenkins `control.go` | F2 | yes |
| M0 | 1 | monitor (all files), config, main | F1, P1 | |
| T1 | 1 | catalog | F1 | |
| T2 | 1 | widgets | – | |
| T3 | 1 | trigger tests | – | |
| T4 | 1 | core loading | F1 | |
| T5 | 1 | docs/adr | owner input | |
| G1 | 1 | jenkins `client.go`, catalog | F2, T1 | yes |
| G2 | 1 | trigger, paramsmerge | T3 | |
| G3 | 1 | params | T3 | |
| G4 | 1 | catalog | T1 (same agent) | |
| G5 | 1 | docs text | – | O1 |
| MD1 | 2 | monitor `render.go` | M0 | |
| MD2 | 2 | monitor `render.go` | JC1 | |
| MD3 | 2 | monitor `render.go`, `history.go` | JC1, M0, MA (one `case`) | |
| LV1 | 2 | monitor `logview.go` | M0 | |
| LV2 | 2 | monitor `logview.go` | LV1 | |
| LV3 | 2 | monitor `logview.go` | LV2 | |
| LV4 | 2 | monitor `logview.go` | M0 | |
| MA1 | 2 | monitor `monitor.go`, `main.go` | M0 | |
| MA2 | 2 | monitor `monitor.go` | MA1 | |
| MA3 | 2 | monitor `monitor.go`, `testreport.go` | JC2, MA2 | yes |
| MA4 | 2 | monitor `monitor.go` | JC3, MA3 | yes |
| MA5 | 2 | monitor `monitor.go` | MA4 | O3 |
| UI1 | 2 | catalog, collections | P1, T1, G4, G1 | |
| CORE1 | 3 | main, monitor key, trigger wiring | JC1, MA5 | yes |
| CORE2 | 3 | main, entry | – | |
| CORE3 | 3 | new `internal/ui/overview`, entry menu | – | |
| CORE4 | 3 | core Router, new palette | CORE3 | |
| CORE5 | 3 | main, collections Deps | – | |
| CORE6 | 3 (or 1) | core theme | F1 | |
| I1 | 4 | docs, wiki, screenshots | all | |
| O1–O4 | 4 | owner | – | – |
