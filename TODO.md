# TODO

Work left for jenktui v1, then known gaps and ideas for later. Tick items off as they land. Terminology is in [GLOSSARY.md](GLOSSARY.md).

## Release status

`v0.1.1` is the current release (six archives plus `checksums.txt`), CI is green, `install.sh` installs it, and the Homebrew cask is published in the tap (replaced by a formula from `v0.1.3`). `v0.1.0` was tagged before the cask config existed, so it has no cask; nothing else differs.

- [x] Push, and CI green on every commit pushed so far.
- [x] Release setup: `jenktui --version`, `.goreleaser.yaml`, `.github/workflows/release.yml`; the first release ran successfully.
- [x] Homebrew tap repo `dursunkoc/homebrew-tap` created, and the `HOMEBREW_TAP_TOKEN` secret saved (the secret's value and permissions are untested until a release uses it).
- [x] `install.sh` tested against the real release: latest-release lookup, a pinned version, a strict POSIX shell.
- [x] Checked against the real Jenkins by the owner, who reports it works. The checklist below was not itemised, so keep using it as a guide for anything that looks off.
- [x] Homebrew cask published by the `v0.1.1` release; the release job used `HOMEBREW_TAP_TOKEN` successfully, and the cask's checksums match the release files.
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

- If `parent_job` is itself a Multibranch Project, its branches are listed as plain jobs and the Branch Filter does not apply.
- A job with no job-specific parameters cannot override a Common Parameter, because its per-job step is skipped.
- The text cursor in parameter forms is solid, not blinking.
- The catalog's "N of M jobs" count can include a Branch Filter placeholder row while the text filter is active.
- Deleting a Collection leaves its monitor in memory. It is re-synced if a new Collection reuses the name.
- Run Monitor state lives only for the session; nothing survives a restart.
- The Run Monitor's header spinner flashes on every automatic poll (every 3 seconds while builds run). If that distracts, show it only for manual `R`.
- Job keys keep Jenkins' URL-encoded branch names (`app/feature%2Fx`), so a hand-edited `collections.toml` must use that form.

## Tech debt

- [ ] The catalog has its own `statusFromColor` with a TODO to use `jenkins.StatusFromColor`. Switch and delete the copy.
- [ ] The list widget's filter box is hand-written. `bubbles/textinput` is now available in `go.mod`, so it could replace it.
- [ ] `core.Status` has no unstable or aborted values, so the monitor draws its own labels for them. Add them to the shared theme.
- [ ] `core.Loading`'s tests cannot detect a replayed first spinner tick, because the spinner's public API hides its tag.
- [ ] The fake Jenkins ignores the `tree=` query, so a mistake in a query would pass the tests. Make the fake honour `tree=`, or add a recorded real response as a fixture.
- [ ] The trigger tests take about 8 seconds because the harness waits out cursor-blink commands.

## Decisions you may want written down

- [ ] An ADR for the stack choice (Go with Bubble Tea over Python with Textual). Rewriting later would be costly and the trade-off was real.

## Later (out of v1)

- Console log streaming.
- Abort and rerun a build.
- Several Jenkins servers or profiles.
- Credential storage in the OS keychain.
- A Branch Filter per Multibranch Project instead of one global list.
- An overview of every Collection's failures from the main menu.
- Keep Run Monitor state across restarts.
