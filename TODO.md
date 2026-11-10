# TODO

Work left for jenktui v1, then known gaps and ideas for later. Tick items off as they land. Terminology is in [GLOSSARY.md](GLOSSARY.md).

## Before the first release

- [ ] **Push and check CI.** `main` on GitHub has the first commit; the later local commits (version flag, release setup, Homebrew and install script) still need pushing. Check the CI run (`.github/workflows/ci.yml`) goes green. It has never run.
- [ ] **Set up Homebrew publishing.** Create an initialised public repo `dursunkoc/homebrew-tap`, create a fine-grained token with Contents read and write on it, and save it as the secret `HOMEBREW_TAP_TOKEN` in the `jenktui` repo (Settings → Secrets and variables → Actions). Do this before the first tag, or the release job fails after creating the release.
- [ ] **Check against the real Jenkins** (the test server is a fake). See the checklist below. Fix whatever differs.
- [ ] **End-to-end tests.** Drive whole flows against `jenkinstest`: splash → login → Collection → trigger (single and multi-job with Common Parameters) → Run Monitor. Each screen already has its own unit tests, so these only cover the joins between them.
- [x] **Release setup.** `jenktui --version`, `.goreleaser.yaml` and `.github/workflows/release.yml` are in place and a local snapshot build of all six platforms works.
- [ ] **Tag `v0.1.0`** once the items above are done. See "Releasing" in [docs/HANDOFF.md](docs/HANDOFF.md). The release workflow has never run, so check the first release page afterwards.
- [ ] **After the first release, test the install paths on a clean machine or account:** `brew install dursunkoc/tap/jenktui` (the generated cask could not be run through `brew audit` here), and `curl -fsSL …/install.sh | sh` (its "latest release" lookup is untested until a release exists), on macOS and Linux.

### Real-Jenkins checklist

Things the fake server cannot prove. Report any mismatch and the fixtures get fixed too.

- [ ] Login succeeds with a username and API token, and a wrong token shows the authentication hint.
- [ ] The Job Catalog lists your folders and Multibranch Projects correctly, and the status badges match each job's last build.
- [ ] Branch names decode correctly (`feature%2Fx` shown and matched as `feature/x`). A project with no matching branches shows the dim row.
- [ ] Triggering works with your credentials: a plain job, a parameterized job, and a Multibranch branch. The crumb and session-cookie fix is unverified; if a trigger fails, the error now carries Jenkins' own text.
- [ ] Parameter types and defaults load correctly (string, bool, choice, password, text).
- [ ] The Run Monitor shows stage rows for a Pipeline job. Parallel stages may arrive as separate top-level stages and not as children of one stage, which is how the client currently reads them.
- [ ] A Collection with many jobs polls without noticeable lag (every job every 30 seconds, running jobs every 3).
- [ ] Jobs that have never built show the unknown badge.

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
