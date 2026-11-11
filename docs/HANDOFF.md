# Handoff

For whoever continues jenktui (a person or an agent). Read this first, then [TODO.md](../TODO.md) for what is left, [GLOSSARY.md](../GLOSSARY.md) for terms, and [ui-contract.md](ui-contract.md) before touching a screen.

## What it is

A Go 1.27 terminal UI for Jenkins built on Bubble Tea. The user supplies a Jenkins URL and an optional parent folder in a config file, logs in with a username and a password or API token, browses every job under that folder, curates named **Collections**, triggers builds from them, and watches each Collection's latest builds in a **Run Monitor**. The v1 feature set is complete and unit-tested. It has not yet been run against a real Jenkins (see the checklist in TODO.md).

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
| `internal/ui/widgets` | Filterable multi-select list (text filter with wildcards, status filter, reorder). |
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
- **Keys:** reuse `core.KeyUp/Down/Enter/Back/Filter/Quit`. In a list, text typed into the filter box includes spaces; `↓` moves focus to the list and `↑` on the first row returns to the box.
- **Styling:** use the theme, never literal colours. Selected rows are bold accent, the cursor row is reverse video.
- **Tests:** plain `go test`, driving `Update` with messages against `jenkinstest`; no real terminal and no sleeping. `e2e_test.go` in the root package drives the whole app through `newDeps` (the production wiring) using helpers in `e2e_helpers_test.go` (`newApp`, `key`, `typeText`, `screenText`, ...). Timers are dropped, so advance the monitor with `R`. Add a scenario there when a change spans screens. Anything time-based takes an injectable clock. Run `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` before committing; CI also runs `-race`.
- **Docs:** when behaviour changes, update README (keys, config), GLOSSARY (terms only, no implementation detail) and `docs/ui-contract.md` (if `core` changes) in the same change.

## Jenkins facts that bit us or will

- **CSRF crumb:** on Jenkins 2.176+ the crumb is tied to the web session. The client keeps a cookie jar so the session cookie from `/crumbIssuer` returns with the POST. If the crumb endpoint refuses (403), the trigger proceeds without one and lets the POST decide. A 403 on the POST is reported as `ErrForbidden` with Jenkins' own text; only 401 means bad credentials. This fix is unverified against the real server.
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

## Releasing

Releases are built by goreleaser from a pushed tag. The version comes from the tag; nothing in the code is edited to release.

1. `main` is green in CI and the real-Jenkins checklist in TODO.md is done.
2. Tag and push: `git tag -a v0.1.0 -m "jenktui v0.1.0" && git push origin v0.1.0`.
3. `.github/workflows/release.yml` runs `go vet` and `go test`, then goreleaser builds archives for macOS, Linux and Windows (amd64 and arm64), writes `checksums.txt`, and creates the GitHub release with a generated changelog. Tags with a suffix such as `-rc1` are marked as pre-releases; a plain `v0.1.0` is a normal release.
4. Check the release page: six archives, the checksums, and `jenktui --version` from one of them.

Homebrew and the install script:

- Each release also updates the cask `jenktui` in the tap repo `github.com/dursunkoc/homebrew-tap` (users run `brew install dursunkoc/tap/jenktui`). This needs, once: an initialised public repo `dursunkoc/homebrew-tap`, and a fine-grained token with Contents read and write on that repo, saved as the secret `HOMEBREW_TAP_TOKEN` in the `jenktui` repo. Without the secret the release job fails after the GitHub release has been created.
- goreleaser's `brews` option is deprecated; the config uses `homebrew_casks`. Its generated cask has not been run through `brew audit` (the check could not run on the author's network), so check `brew install dursunkoc/tap/jenktui` after the first release.
- `install.sh` (macOS and Linux, no Go needed) downloads the latest release archive, verifies it against `checksums.txt`, and installs to `/usr/local/bin` or `~/.local/bin`. `JENKTUI_VERSION`, `INSTALL_DIR` and `JENKTUI_BASE_URL` override its defaults (the last one is for testing against a local server). Its "latest release" lookup has only been exercised once a real release exists.

To try the build without publishing anything, run `goreleaser release --snapshot --clean` (output goes to the git-ignored `dist/`). `goreleaser check` validates the config.

A pushed tag is cached by Go's module proxy and should not be moved. If a release is wrong, fix it and publish the next patch version. Versions are shown by `internal/version`: release builds get them from linker flags, `go install module@vX.Y.Z` builds from Go's build info, and other builds show `dev`.
