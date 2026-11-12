# TODO

Work left for jenktui v1, then known gaps and ideas for later. Tick items off as they land. Terminology is in [GLOSSARY.md](GLOSSARY.md).

## Release status

`v0.1.4` is the current release (six archives plus `checksums.txt`), CI is green, `install.sh` installs it, and the Homebrew tap holds a formula (a cask until `v0.1.2`). `v0.1.0` was tagged before the Homebrew config existed, so it has none.

- [x] Push, and CI green on every commit pushed so far.
- [x] Release setup: `jenktui --version`, `.goreleaser.yaml`, `.github/workflows/release.yml`; the first release ran successfully.
- [x] Homebrew tap repo `dursunkoc/homebrew-tap` created, and the `HOMEBREW_TAP_TOKEN` secret saved (the secret's value and permissions are untested until a release uses it).
- [x] `install.sh` tested against the real release: latest-release lookup, a pinned version, a strict POSIX shell.
- [x] Checked against the real Jenkins by the owner, who reports it works. The checklist below was not itemised, so keep using it as a guide for anything that looks off.
- [x] Homebrew cask published by the `v0.1.1` release; the release job used `HOMEBREW_TAP_TOKEN` successfully, and the cask's checksums match the release files.
- [ ] Run the Windows PowerShell install steps (README, wiki Getting Started) on a Windows machine; they were written without one.
- [ ] Test `brew install dursunkoc/tap/jenktui` on macOS and, if possible, Linux. The tap now holds a formula (from `v0.1.3`); the cask upgrade to `v0.1.2` worked on the owner's Mac, the formula is untried.
- [x] End-to-end tests (`e2e_test.go`, 11 scenarios, about 2s) cover the joins between screens through the real wiring in `main.go`. Not covered: the monitor's automatic 3s/30s schedule (timers are dropped in the harness), the spinner animation, and a job deleted from Jenkins between loading and triggering (the fake cannot delete jobs).

### Real-Jenkins checklist

Things the fake server cannot prove. If any of these looks wrong on your Jenkins, report the exact behaviour and fix the fixtures too.

- Login succeeds with a username and API token, and with a username and password (including triggering a build, which needs a CSRF crumb), and a wrong one shows the authentication hint.
- The Job Catalog lists your folders and Multibranch Projects correctly, and the status badges match each job's last build.
- Branch names decode correctly (`feature%2Fx` shown and matched as `feature/x`). A project with no matching branches shows the dim row.
- Triggering works: a plain job, a parameterized job, and a Multibranch branch. If a trigger fails, the error carries Jenkins' own text.
- Parameter types and defaults load correctly (string, bool, choice, password, text).
- The Run Monitor shows stage rows for a Pipeline job. Parallel stages may arrive as separate top-level stages and not as children of one stage, which is how the client currently reads them.
- A Collection with many jobs polls without noticeable lag (every job every 30 seconds, running jobs every 3).
- Jobs that have never built show the unknown badge.

## Known gaps in v1

- Run Monitor state lives only for the session; nothing survives a restart.
- Job keys keep Jenkins' URL-encoded branch names (`app/feature%2Fx`), so a hand-edited `collections.toml` must use that form.

- The console log has no horizontal scroll; `w` wraps long lines, but a single line taller than the screen shows only its first screenful. It caps at 10,000 lines (numbering keeps counting; the error count covers the kept lines) and has no `?` help panel, since `?` is typeable in the find prompt.
- Notifications fire only while that Collection's Run Monitor is the screen on show: the Router delivers replies only to the top screen, so a monitor you left stops polling. Notifying from a monitor not on show needs a Router change.
- Saving a very large log still reads Jenkins' first response whole into memory; a streaming client method (`ConsoleTo`) would avoid it.
- `o`/`y` copy over OSC 52 when no clipboard tool is available; some terminals drop large OSC 52 payloads silently, so `y` on a huge line may say "sent to terminal clipboard" and copy nothing.
- A running build's stage or step log is refetched whole every 3 seconds (the stage endpoint has no byte offset), which is wasteful for very long stages.
- The README screenshots are generated (see HANDOFF) and do not update on their own; regenerate them when a screen changes.

## Tech debt

- [ ] Screens that load or post without being a `core.Pauser` report `Busy()` so `Ctrl+P` waits; a new screen with an async one-shot request must do the same (see `docs/ui-contract.md`).
- [ ] Monitor counters use per-monitor ranges (`base << 32`), which assumes a 64-bit `int`; releases build only amd64 and arm64.

- [x] Replies reach only the top screen, so a screen pushed over the Run Monitor (for example a `Ctrl+P` palette, CORE4) would swallow its poll results and fetches without bumping its epoch: `pending` would never reach zero and lost-reply detection would not fire. Bump the monitor's epoch when covering it, or forward its messages. Also, two monitors could in theory accept each other's poll results if `gen`, job and `rev` match.
- [ ] In the test report overlay `q` does not quit, while in the log view it does; pick one rule.

- [x] `trigger.go` and `control.go` each have their own POST-with-crumb loop; extract a shared helper (plan: CORE5).
- [x] Under a Multibranch `parent_job`, a job the Run Monitor or trigger screen has no name for falls back to `core.RelName` and shows the encoded branch (`feature%2Fx`).

- [x] The catalog has its own `statusFromColor` with a TODO to use `jenkins.StatusFromColor`. Switch and delete the copy.
- [x] The list widget's filter box is hand-written. `bubbles/textinput` is now available in `go.mod`, so it could replace it.
- [x] `core.Status` has no unstable or aborted values, so the monitor draws its own labels for them. Add them to the shared theme.
- [x] `core.Loading`'s tests cannot detect a replayed first spinner tick, because the spinner's public API hides its tag.
- [x] The fake Jenkins ignores the `tree=` query, so a mistake in a query would pass the tests. Make the fake honour `tree=`, or add a recorded real response as a fixture.
- [x] The trigger tests take about 8 seconds because the harness waits out cursor-blink commands.

## Decisions you may want written down

- [ ] An ADR for the stack choice (Go with Bubble Tea over Python with Textual). Rewriting later would be costly and the trade-off was real.

## v0.2

Developer-facing features, in suggested order. [docs/plan-v0.2.md](docs/plan-v0.2.md) breaks these and the other open items into issues for parallel agents. The first group is small and does not touch any settled decision. Update README, GLOSSARY, the wiki Keyboard Reference and the screenshots as each one lands.

### 1. Leaving the app
- [x] `o` opens the highlighted job or build in the browser (`Build.URL` is already fetched; `open` on macOS, `xdg-open` on Linux, `rundll32`/`start` on Windows).
- [x] `y` copies the highlighted build's URL to the clipboard; in the log view, copies the current line or match.

### 2. Why did it fail?
- [x] `Enter` on a failed build lands on the first failed stage, and `l` opens its log scrolled to the end.
- [x] Highlight error lines in the console log (`ERROR`, `FAILED`, `Exception`, `BUILD FAILURE`, …) and add a key that jumps to the next one.
- [x] Test report: read `/testReport/api/json` and show "N failed / M passed" in the details view, with the failed test names and their stack traces. 404 means no report; show nothing.

### 3. Notifications
- [x] When a watched build finishes, ring the terminal bell and/or send a desktop notification (`osascript` / `notify-send`), configurable, optionally only on failure. Only for builds the monitor saw running, not on its first load.

### 4. Abort and rerun
This reverses "abort and rerun" being out of v1 (see HANDOFF, settled decisions); update HANDOFF when it lands.
- [x] Rerun the highlighted build with the same parameters (`r`), read from the build's `actions[parameters]`; password parameters cannot be read back, so ask for them.
- [x] Abort a running or queued build (`X`, with a confirmation): `POST .../stop` for a build, `/queue/cancelItem?id=` for a queue item. Both need the CSRF crumb.

### 5. Build context
- [x] Client part done: `BuildStatus` has `estimatedDuration`; `BuildContext` returns causes, changes and parameters; `BuildHistory` the last results. Left: the UI below.
- [x] Monitor: a progress bar or ETA for running builds ("1m36s / ~4m") and when each build started ("2m ago").
- [x] Details view: who started the build and which commits it contains.
- [x] A short result history per job (`●●●○●`, the last five builds) to spot flaky jobs.

### 6. Getting around faster
- [x] Command palette or fuzzy jump (`Ctrl+P`): type part of a job or Collection name, `Enter` to open it, from any screen.
- [x] `jenktui <collection>` opens that Collection's Run Monitor straight after login.
- [x] An overview of every Collection's failures and running builds from the main menu (moved from Later).

### 7. Log viewer
- [x] Save the log to a local file (`S`), written to the current directory as `<job>-<n>.log`, with the path shown (moved from Later: "Download logs to local upon request").
- [x] Wrap toggle (`w`) or horizontal scroll for long lines (see Known gaps).

### 8. Polish
- [x] Show the monitor's header spinner only for a manual `R`, not on every automatic poll (see Known gaps).
- [x] Respect `NO_COLOR`, and offer a light theme.

### Needs a decision
- [ ] Reading the API token from an environment variable or a command (`token_command = "op read …"`, `pass jenkins`), so jenktui still stores nothing but no one has to paste the token on every start. This touches the "credentials are never stored" decision; the owner decides.

## Later (out of v1)

- Several Jenkins servers or profiles.
- Credential storage in the OS keychain.
- A Branch Filter per Multibranch Project instead of one global list.
- Keep Run Monitor state across restarts.
