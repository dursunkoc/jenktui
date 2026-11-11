# Handoff

For whoever continues jenktui (a person or an agent). Read this first, then [TODO.md](../TODO.md) for what is left, [GLOSSARY.md](../GLOSSARY.md) for terms, and [ui-contract.md](ui-contract.md) before touching a screen.

## What it is

A Go 1.27 terminal UI for Jenkins built on Bubble Tea. The user supplies a Jenkins URL and an optional parent folder in a config file, logs in with a username and a password or API token, browses every job under that folder, curates named **Collections**, triggers builds from them, and watches each Collection's latest builds in a **Run Monitor**. The v1 feature set is complete.

## Current state

- **Released:** `v0.1.2` (six archives, macOS/Linux/Windows on amd64 and arm64, plus `checksums.txt`), which adds the UI improvements (aligned list columns with titles, relative job names, help panel, password login). The Homebrew cask was updated by the release job. `v0.1.1` and `v0.1.0` exist too; `v0.1.0` predates the cask.
- **Install channels:** Homebrew cask in `dursunkoc/homebrew-tap` (`brew install dursunkoc/tap/jenktui`), `install.sh` (checked against the real release), release archives, `go install`. The Homebrew install itself has not been tried by anyone yet.
- **Verified:** CI is green (vet, gofmt, `go test -race`); there are unit tests per package and an end-to-end suite (`e2e_test.go`) against the fake Jenkins. The owner ran the app against their real Jenkins and reported it works. They did not itemise the checklist in TODO.md, so a server that behaves differently (parallel pipeline stages, Multibranch layouts, crumb settings) may still surprise.
- **Not covered by tests:** the monitor's automatic 3s/30s polling schedule, spinner animation, and a job deleted from Jenkins between loading and triggering.
- **Open work:** [TODO.md](../TODO.md): tech debt, known gaps in v1, and ideas for later.
- **Elsewhere:** the user wiki is a separate repo, `github.com/dursunkoc/jenktui.wiki`, kept locally beside this one at `../jenktui.wiki` (pages: Home, Getting Started, Configuration, Collections and the Run Monitor, Filtering, Keyboard Reference, Troubleshooting, Development). Update it when user-facing behaviour changes, and push it separately.

## Layout

| Path | Role |
|---|---|
| `main.go` | Composition root. Wires screens together: Menu → Catalog or Collections → Trigger → Run Monitor. Holds one monitor per Collection for the session. |
| `internal/config` | `config.toml` (hand-edited settings) and `collections.toml` (app-written). Atomic writes, `0600`. No secrets. |
| `internal/jenkins` | Jenkins JSON API client: login check, job listing (recursive, Multibranch aware), parameter definitions, trigger with CSRF crumb, queue resolution, build status, pipeline stages. |
| `internal/jenkins/jenkinstest` | Fake Jenkins for tests (`httptest`). Models folders, Multibranch, parameters, queue, wfapi stages, crumbs. |
| `internal/branchfilter` | Pure glob matcher and filter for Multibranch branch names. |
| `internal/paramsmerge` | Pure logic that finds Common Parameters across jobs and resolves final values per job. |
| `internal/ui/core` | `Screen` interface, `Router`, theme, keymap, `Loading` spinner. Frozen contract, documented in `docs/ui-contract.md`. |
| `internal/ui/widgets` | Filterable multi-select list (text filter with wildcards, status filter, reorder) with name, branch and status columns. A blank line, the filter row, a blank line and the column titles (Pipeline, Branch, Status) sit above the rows. |
| `internal/ui/entry` | Splash, first-run setup, login, menu. |
| `internal/ui/catalog` | Job Catalog screen and the shared Branch Filter panel. |
| `internal/ui/collections` | Collections list, opened Collection view, picker. |
| `internal/ui/params`, `internal/ui/trigger` | Parameter form widgets and the single/multi-job trigger flow. |
| `internal/ui/monitor` | Per-Collection Run Monitor and its polling. |

Screens receive their dependencies through constructors (listers, factories, file paths) and never import each other. `main.go` is the only place that knows the whole graph. Keep it that way: it is what makes each screen testable with a fake.

## Decisions that are settled

These were agreed with the user. Change them only if the user asks.

- **Credentials are never stored.** Username and password or API token are asked on every start and live in memory inside the client.
- **Go with Bubble Tea** (the user chose it over Python with Textual).
- **Config:** TOML, `~/.config/jenktui/`, overridable with `JENKTUI_CONFIG_DIR`. Settings and Collections are separate files so the app never rewrites the hand-edited one except for Branch Filter patterns.
- **Branch Filter** is one global list of case-sensitive globs on the decoded branch name. It only affects browsing and picking, never branches already in a Collection.
- **Run Monitor belongs to a Collection.** It tracks the latest build of every job in it, hides successful jobs by default (`s` toggles), polls every job at 30s and running or queued ones at 3s. It is not a global feature.
- **Missing jobs** stay in a Collection, marked missing, skipped when triggering, and are never removed silently.
- **Out of v1:** console log streaming, abort and rerun, multiple servers, credential storage.

## Conventions

- **Bubble Tea:** all async work is a `tea.Cmd`; no goroutines of our own. Screens that poll or fetch use a generation counter (monitor) or sequence number (collections) and drop stale messages. Spinner ticks stop when nothing is loading, so idle screens do not redraw.
- **Loading feedback:** blocking `Modal` when a screen has nothing to show yet, header spinner when old data stays visible (`core.Loading`, 500ms minimum visible time).
- **Keys:** reuse `core.KeyUp/Down/Enter/Back/Filter/Quit`. Screens with many keys implement `core.FullHelper`: `Keys()` returns only the few main ones for the help bar and `FullHelp()` groups all of them for the `?` panel (see `docs/ui-contract.md`). In a list, text typed into the filter box includes spaces; `↓` moves focus to the list and `↑` on the first row returns to the box.
- **Job names** are shown relative to the Parent Job, and a Multibranch branch as `project › branch` with the decoded branch name: build them with `catalog.JobName(job, parent)` (a `core.JobName`) or `catalog.Row`. List items keep the full name as `Key` (filtering, selection, saved Collections) and put the project in `Label`, the branch in `Branch`; the list draws name, branch and badge as aligned columns sized to all its items, with the folders in the name column separated by a muted " › ". In e2e tests a job path such as `team/api/build` also matches that list form (helper `has`), so one expectation works on lists and on the Run Monitor and trigger screens, which print the path. The Run Monitor gets the names through `monitor.SetNames` and the trigger screen through `trigger.Deps.Names`; a job they have no name for is shown with `core.RelName`.
- **Styling:** use the theme, never literal colours. Selected rows are bold accent, the cursor row is reverse video.
- **Tests:** plain `go test`, driving `Update` with messages against `jenkinstest`; no real terminal and no sleeping. `e2e_test.go` in the root package drives the whole app through `newDeps` (the production wiring) using helpers in `e2e_helpers_test.go` (`newApp`, `key`, `typeText`, `screenText`, ...). Timers are dropped, so advance the monitor with `R`. Add a scenario there when a change spans screens. Anything time-based takes an injectable clock. Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` before committing; CI also runs `-race`.
- **Docs:** when behaviour changes, update README (keys, config), GLOSSARY (terms only, no implementation detail) and `docs/ui-contract.md` (if `core` changes) in the same change.

## Jenkins facts that bit us or will

- **CSRF crumb:** on Jenkins 2.176+ the crumb is tied to the web session. The client keeps a cookie jar so the session cookie from `/crumbIssuer` returns with the POST. If the crumb endpoint refuses (403), the trigger proceeds without one and lets the POST decide. A 403 on the POST is reported as `ErrForbidden` with Jenkins' own text; only 401 means bad credentials. The owner's Jenkins accepts triggers with this approach; servers with other crumb or proxy settings may behave differently, and the error text now says why.
- **Queue items** return 404 shortly after a build starts. `ResolveQueue` reports `ErrQueueItemGone`, and the monitor then falls back to the job's last build if it started after the trigger.
- **Pipeline stages** come from `/wfapi/describe`. Freestyle jobs and servers without the plugin return 404 (`ErrNoStages`), which the UI treats as "badge only". Parallel stages are read as children of one stage; real Jenkins may report them differently.
- **Multibranch:** detected by `_class` containing `WorkflowMultiBranchProject`. Branch job names are URL-encoded (`feature%2Fx`) and stay encoded in job keys and in `collections.toml`; the decoded name is only for matching and display.
- **`tree=` queries:** real Jenkins honours them and the fake ignores them. Every field the client decodes must be named in its query.
- **Status badges** come from the job's `color` field (`StatusFromColor`); the Run Monitor uses each job's last build result instead.

## Working with this repo

- Go lives at `/opt/homebrew/bin/go` on the author's machine (installed with Homebrew); add it to `PATH` in non-interactive shells.
- The binary is built with `go build -o jenktui .` and is git-ignored.
- Parallel work went well when each task owned a disjoint package and the `core` contract stayed fixed. Keep `main.go` and `docs/ui-contract.md` edits with one owner at a time.
- The repo's remote is `https://github.com/dursunkoc/jenktui.git`. Do not push or create releases without the owner asking.
- **Git identity:** this repo has a repo-local identity, `dursunkoc <dursunkoc@gmail.com>`. The owner's global git identity is a work address that should not appear on this public repo, so keep the local setting and do not commit from another checkout without it.
- **`refs/notes/ai`:** a `git-ai` tool in the owner's environment attaches notes to commits and pushes that ref. It is not part of any branch and can be ignored.
- **GitHub tokens:** pushes use the owner's own credentials. Never ask for, paste or store a token. The release job uses the repo secret `HOMEBREW_TAP_TOKEN` (fine-grained, Contents read and write on the tap only); if releases start failing at the Homebrew step, check whether it has expired.

## Releasing

Releases are built by goreleaser from a pushed tag. The version comes from the tag; nothing in the code is edited to release.

1. `main` is green in CI and the real-Jenkins checklist in TODO.md is done.
2. Tag and push, with the next version number: `git tag -a vX.Y.Z -m "jenktui vX.Y.Z" && git push origin vX.Y.Z`.
3. `.github/workflows/release.yml` runs `go vet` and `go test`, then goreleaser builds archives for macOS, Linux and Windows (amd64 and arm64), writes `checksums.txt`, and creates the GitHub release with a generated changelog. Tags with a suffix such as `-rc1` are marked as pre-releases; a plain `v0.1.0` is a normal release.
4. Check the release page: six archives, the checksums, and `jenktui --version` from one of them.

Homebrew and the install script:

- Each release also updates the cask `jenktui` in the tap repo `github.com/dursunkoc/homebrew-tap` (users run `brew install dursunkoc/tap/jenktui`). This needs, once: an initialised public repo `dursunkoc/homebrew-tap`, and a fine-grained token with Contents read and write on that repo, saved as the secret `HOMEBREW_TAP_TOKEN` in the `jenktui` repo. Without the secret the release job fails after the GitHub release has been created.
- goreleaser's `brews` option is deprecated; the config uses `homebrew_casks`. Its generated cask has not been run through `brew audit` (the check could not run on the author's network), so run `brew install dursunkoc/tap/jenktui` on a Mac after a release. The cask was published by `v0.1.1` and updated by `v0.1.2`; the checksums match the release files. After a release, `brew update` (not `brew update <name>`) then `brew upgrade --cask dursunkoc/tap/jenktui` picks it up.
- `install.sh` (macOS and Linux, no Go needed) downloads the latest release archive, verifies it against `checksums.txt`, and installs to `/usr/local/bin` or `~/.local/bin`. `JENKTUI_VERSION`, `INSTALL_DIR` and `JENKTUI_BASE_URL` override its defaults (the last one is for testing against a local server). It has been run against the real `v0.1.0` and `v0.1.1` releases, including the latest-release lookup and a pinned version.

To try the build without publishing anything, run `goreleaser release --snapshot --clean` (output goes to the git-ignored `dist/`). `goreleaser check` validates the config.

A pushed tag is cached by Go's module proxy and should not be moved. If a release is wrong, fix it and publish the next patch version. Versions are shown by `internal/version`: release builds get them from linker flags, `go install module@vX.Y.Z` builds from Go's build info, and other builds show `dev`.
