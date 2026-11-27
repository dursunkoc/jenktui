# TODO

Work left for jenktui v1, then known gaps and ideas for later. Tick items off as they land. Terminology is in [GLOSSARY.md](GLOSSARY.md).

## Release status

`v0.4.0` is the current release (Parameter Presets; `v0.3.0` added the nested Collections tree, Remember me and terminal notifications) (six archives plus `checksums.txt`), CI is green, `install.sh` installs it, and the Homebrew tap holds a formula (a cask until `v0.1.2`). `v0.1.0` was tagged before the Homebrew config existed, so it has none.

- [x] Push, and CI green on every commit pushed so far.
- [x] Release setup: `jenktui --version`, `.goreleaser.yaml`, `.github/workflows/release.yml`; the first release ran successfully.
- [x] Homebrew tap repo `dursunkoc/homebrew-tap` created, and the `HOMEBREW_TAP_TOKEN` secret saved (the secret's value and permissions are untested until a release uses it).
- [x] `install.sh` tested against the real release: latest-release lookup, a pinned version, a strict POSIX shell.
- [x] Checked against the real Jenkins by the owner, who reports it works. The checklist below was not itemised, so keep using it as a guide for anything that looks off.
- [x] Homebrew cask published by the `v0.1.1` release; the release job used `HOMEBREW_TAP_TOKEN` successfully, and the cask's checksums match the release files.
- [x] Run the Windows PowerShell install steps (README, wiki Getting Started) on a Windows machine; they were written without one.
- [x] Test `brew install dursunkoc/tap/jenktui` on Linux. On macOS the formula works: `brew upgrade` took the owner's Mac from `0.1.4` to `0.2.0`, and then to `0.3.0`.
- [x] End-to-end tests (`e2e_test.go`, 11 scenarios, about 2s) cover the joins between screens through the real wiring in `main.go`. Not covered: the monitor's automatic 3s/30s schedule (timers are dropped in the harness), the spinner animation, and a job deleted from Jenkins between loading and triggering (the fake cannot delete jobs).

### Real-Jenkins checklist

Done: the owner ran this whole checklist against their real Jenkins on 2026-11-27, after the `v0.3.0` release. Things the fake server cannot prove. If any of these looks wrong on your Jenkins, report the exact behaviour and fix the fixtures too.

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
- `y` copies over OSC 52 (inside tmux, `tmux load-buffer -w`) when no clipboard tool is available; some terminals drop large OSC 52 payloads silently, so `y` on a huge line may say "sent to terminal clipboard" and copy nothing.
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
- [x] When a watched build finishes, ring the terminal bell and/or send a desktop notification (`osascript` / `notify-send`), configurable, optionally only on failure. Only for builds the monitor saw queued or running, not on its first load.
- [x] Terminal notifications (OSC 9/777/99) in iTerm2, Ghostty, WezTerm and kitty, so a click brings the terminal forward; returning to the terminal (focus) or `!` opens the last notified failure's log. jenktui's icon (`docs/icon/make_icon.py`) on Linux `notify-send`.

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

### Before releasing v0.2.0
- [x] Integration pass (plan I1): README, GLOSSARY, `docs/ui-contract.md` and HANDOFF agree with the code; wiki pages updated; screenshots regenerated, with a new Overview shot.
- [x] Real-Jenkins check of the new features (plan O3): matrix and Maven test reports, stop and cancel, commits on freestyle jobs, causes and parameters, the folder colours and queue names the Overview reads, notifications on your desktop, copy over SSH and in tmux.
- [x] Push `main` and the wiki, then release `v0.2.0` as in HANDOFF ("Releasing"). Released before the real-Jenkins check above, at the owner's request; the tap formula installed `0.2.0` on the owner's Mac.

### Nested Collections
- [x] Collections nest to any depth (folders hold jobs and sub-folders; a job may be in several). Folder Run Monitor, Trigger and Overview count cover the subtree; `jenktui a/b`, `Ctrl+P` and move/copy/delete-with-counts handle paths. See GLOSSARY "Collection" and `docs/ui-contract.md` "Folders".
- [x] The Collections screen is one expandable tree (empty-folder creation, folders and jobs together, expand/collapse); spec in `docs/spec-collections-tree.md`.
- [x] Real-Jenkins check of a deep tree; the README screenshot is regenerated as a tree.
- Known gap: a hand-edited file in the old flat shape keeps working, but a top-level name that contains `/` cannot be addressed from the command line until it is renamed.

### Parameter presets
- [x] Named sets of parameter values, saved per job and applied from a preset step in the Trigger flow (moved from Later). A name spans jobs, passwords are never saved, `s` saves from the summary and `d` deletes. Spec: [docs/spec-presets.md](docs/spec-presets.md); plan: [docs/plan-presets.md](docs/plan-presets.md). Implemented and tested against the fake Jenkins; released in `v0.4.0`.
- [ ] Real-Jenkins check of presets: save, apply to one job and to a folder, a choice that no longer exists, and a job with a password parameter.

### Needs a decision
- [ ] Reading the API token from an environment variable or a command (`token_command = "op read …"`, `pass jenkins`), so jenktui still stores nothing but no one has to paste the token on every start. This touches the "credentials are never stored" decision; the owner decides.

## Later (out of v1)

- Several Jenkins servers or profiles.
- Credential storage in the OS keychain.
- A Branch Filter per Multibranch Project instead of one global list.
- Keep Run Monitor state across restarts.
- Notify from a Run Monitor that is not on show (needs a Router change; see Known gaps).
- jenktui's icon on macOS notifications. macOS takes the icon from the sending app, so it needs an app bundle of jenktui's own; an unsigned one built at run time was tried and is blocked by corporate endpoint-privilege tools (they ask for admin rights). It would need a Developer ID signed and notarised notifier shipped with releases.
- Check that Apple Terminal sends focus events (`tea.WithReportFocus`); without them only `!` jumps to the failure.

Features a developer who lives in Jenkins all day would expect and that jenktui does not have yet. These are ideas, not commitments.

### Triggering

- **Trigger with the last values.** One key refills the form from the job's previous build, for any job, not only as a rerun from the Run Monitor.
- **Ordered runs.** Trigger a folder's jobs one after another, in list order, and stop at the first failure (build, then test, then deploy).
- **Replay.** Rerun a Pipeline build with an edited Jenkinsfile, as Jenkins' Replay does.
- **Trigger from the command line.** `jenktui trigger backend/develop --param ENV=staging --wait` runs without the UI and exits non-zero on failure, for scripts and git hooks.

### Watching builds

- **Answer `input` steps.** Show a Pipeline that waits for approval, and approve or abort it from the Run Monitor. Today such a build just looks stuck.
- **A queue screen.** Everything waiting on the server, why each item waits (no free agent, a quiet period, a blocked upstream), and a key to cancel it.
- **"My builds".** One list of the builds started by the logged-in user across every job, whether or not they are in a Collection.
- **Watch a branch.** Give a branch name and see that branch's build in every Multibranch Project that has it. This fits a feature branch that spans several repos.
- **Estimated time left.** Show a countdown or progress bar per stage from past stage durations, not only for the whole build.
- **Follow downstream builds.** When a build triggers others, show them as children and open them from the parent.

### Understanding failures

- **Compare with the last good build.** Show which commits, parameters and stage durations differ between a failed build and the last success of the same job.
- **Flaky test hints.** In the test report, mark a test that failed and passed in recent builds without a code change.
- **Search across logs.** Search for a string in the latest logs of every job in a folder, to see whether the same error hit several jobs.
- **Error patterns.** Configure regular expressions that count as errors in a log, in addition to the built-in ones, with a jump key between matches.
- **Artifacts.** List a build's artifacts, download one, or copy its link.
- **Log timestamps.** Show per-line times in the console log, from the Timestamper plugin (dropped earlier at the owner's request; listed in case it returns).

### Jobs and the server

- **Agents and executors.** Show which agents are online, offline or busy, and which build each executor runs. It answers "why is my build queued?".
- **Enable and disable a job**, and see at a glance that a job is disabled.
- **Scan a Multibranch Project now**, so a new branch shows up without waiting for the next scheduled scan.
- **Job health.** Show a success rate and average duration over the last N builds per job, and for a folder as a whole.
- **Favourite jobs.** Pin a job in the Job Catalog without building a Collection for it.
- **View the job's configuration**: its parameters, triggers, and the Jenkinsfile path and repository of a Pipeline job (read-only).

### Collections

- **Export and import.** Share a folder with a team-mate as a file or a pasted snippet, with a merge on import.
- **Dynamic folders.** Define a folder by a pattern (`team/*/deploy`, or "every job whose last build failed"), not a fixed list, and keep it current as jobs come and go.
- **Persist the expansion state** of the tree across restarts (left out of the tree spec on purpose).
- **Copy a folder**, with its sub-folders (also left out of the tree spec).
- **Undo** for delete, move and remove, for the last change at least.

### Comfort

- **Custom key bindings and colours**, in `config.toml`.
- **A status line for other tools.** `jenktui status backend` prints one line (`3 ok · 1 failed · 2 running`) for a shell prompt, tmux or a status bar.
- **Session timeout handling.** When the session or token expires, ask for the password again in place and keep the current screen, without a restart.
- **Mouse support**: click a row, scroll a log.
- **Open in the editor.** Send a log to `$EDITOR` or `$PAGER`, for the search and navigation people already know.
- **Webhook-style alerts.** Post a finished build to Slack or a URL, for people who are away from the terminal.
