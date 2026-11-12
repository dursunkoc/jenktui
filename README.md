# jenktui

A rich terminal UI for Jenkins. Browse every job under a parent folder, curate named **Collections**, trigger builds from them, and watch each Collection's latest builds live, with pipeline stage info.

## Install

None of these need Go, except the last two.

**Homebrew (macOS and Linux):**

```sh
brew install dursunkoc/tap/jenktui
```

**Install script (macOS and Linux):**

```sh
curl -fsSL https://raw.githubusercontent.com/dursunkoc/jenktui/main/install.sh | sh
```

It picks the right build for your system, checks it against the release checksums, and installs `jenktui` to `/usr/local/bin` (or `~/.local/bin` if that is not writable). Set `INSTALL_DIR` to choose another folder and `JENKTUI_VERSION` for a specific version. Read the [script](install.sh) first if you prefer.

**Download by hand (macOS, Linux, Windows):** get the archive for your system from the [Releases](https://github.com/dursunkoc/jenktui/releases) page, unpack it, and put `jenktui` on your `PATH`. On macOS, a file downloaded in a browser is quarantined; clear it once with `xattr -d com.apple.quarantine jenktui`.

**With Go:** `go install github.com/dursunkoc/jenktui@latest`, or build from a checkout with `go build -o jenktui .`

`jenktui --version` prints the version.

## Configuration

`~/.config/jenktui/config.toml` (set `JENKTUI_CONFIG_DIR` to use another directory). On first run, if the file is missing, the app asks for the URL and parent job and writes it for you.

```toml
jenkins_url = "https://jenkins.example.com"
parent_job = ""            # optional; empty = Jenkins root
username = "me"            # optional; prefills the login screen
# ca_cert = "/path/to/ca.pem"
insecure_skip_verify = false
branch_filter = ["main", "release/*"]   # optional; globs for Multibranch branch names
```

Collections are stored in `~/.config/jenktui/collections.toml` (managed by the app).

**Credentials are never stored.** Log in with your Jenkins username and your password or an API token. An API token is recommended: it can be revoked on its own and does not expose your password. Both are asked on every start and kept in memory only, as a note at the bottom of the login screen says.

TLS is verified strictly. For an internal CA, set `ca_cert`; `insecure_skip_verify` is a last resort.

## Flow

Splash → Login → Menu (Collections, Job Catalog, Quit).

- **Job Catalog**: every buildable job under the parent job, flattened and filterable.

Job names are shown relative to the parent job: under `parent_job = "team"`, `team/app/build` is shown as `app/build` (in the catalog, Collections and the Run Monitor). A job outside the parent keeps its full name. Job lists have three aligned columns, titled Pipeline, Branch and Status: the job (for a Multibranch Project, the project) with its folders separated by a grey `›` (`team › api › build`), the branch, decoded (`feature/login` rather than `feature%2Flogin`), and the status. The Run Monitor and the trigger screen show a branch as `digital-ui › feature/login`.
- **Collections**: named, ordered groups of jobs you pick from the catalog. Open one to trigger its jobs or watch them.
- **Trigger**: one job gets a confirmation or a parameter form. Several jobs get Common Parameters first (same name and type, entered once), then each job's remaining parameters, with a per-job override, then a summary to confirm. Passwords are shown as `••••`.
- **Run Monitor**: one per Collection, opened with `m`. It shows the latest build of every job, hides successful ones by default, and shows pipeline stages for running builds. `Enter` opens a build's details and `l` there shows its console log in a modal with line numbers, following new output while the build runs. `↑`/`↓`/`PgUp`/`PgDn` scroll, `g` top, `G` follow again, `:` goes to a line number, `/` finds text (case-insensitive, `*` any text, `_` one character, `\*` `\_` literal; `n`/`N` next/previous match, matches are highlighted), `Esc` back. Leaving it keeps its state for the session.

## Filtering

Press `/` in any list and type. Matching is case-insensitive, anywhere in the job's full path (including the parent job's part, even though it is not shown):

| Type | Matches |
|---|---|
| `*` | any run of characters (including none) |
| `_` | exactly one character |
| `\*` `\_` `\\` | a literal `*`, `_` or `\` |

Press `↓` to move from the filter box into the list (the filter stays applied) and `↑` on the first row to edit the filter again. `s` cycles a status filter (failed, running, queued, success, unknown, missing, off) and combines with the text filter.

### Branch Filter

Limits which branches of Multibranch Projects are shown when browsing or picking. Patterns are case-sensitive globs matched against the decoded branch name (`feature/x`, not `feature%2Fx`); `*` matches any text including `/`, `?` one character, `[a-z]` a set. No patterns means all branches. Folders and ordinary jobs are never filtered, and branches already in a Collection stay there. `b` switches the filter on and off, `B` edits the patterns (saved to `config.toml`).

## Keys

| Where | Keys |
|---|---|
| Everywhere | `↑`/`↓` (or `k`/`j`), `Enter`, `Esc` back, `q` quit, `Ctrl+C` quit |
| Help | The bar at the bottom shows the main keys of the screen. Where it ends in `? all keys`, `?` opens a panel with every key, grouped; `Esc` or `?` closes it |
| Lists | `/` filter, `s` status filter, `Space` select, `a` select all visible, `n` clear selection |
| Filter box | type to filter, `Enter`/`↓` go to the list, `Esc` clear the filter |
| Catalog and picker | `b` Branch Filter on/off, `B` edit patterns |
| Collections list | `Enter` open, `n` new, `e` edit members, `r` rename, `d` delete, `m` monitor |
| Opened Collection | `t` trigger selected (or the highlighted job), `m` monitor, `e` edit members, `x` remove a missing job, `R` refresh, `Shift+↑/↓` reorder |
| Parameter forms | `Tab`/`Shift+Tab` move, `Enter` next/submit, `Esc` back a step |
| Run Monitor | `Enter` details (`↑`/`↓` move between stages, `l` console log with `/` find and `:` go to line), `s` show/hide successful, `R` refresh now, `Esc` back (from details: back to the list) |

A Collection member that no longer exists in Jenkins stays in the Collection, marked missing, and is skipped when triggering.

## Scope

In: job catalog, Collections (create/edit/rename/delete/reorder), triggering with parameters (common + per-job), per-Collection Run Monitor with stages, Branch Filter.

Out of v1: abort/rerun, multiple servers, credential storage.

## Development

```sh
go vet ./... && go test ./...
```

CI runs the same plus `gofmt` and `-race` (`.github/workflows/ci.yml`). Tests run against a fake Jenkins in `internal/jenkins/jenkinstest`; it ignores the `tree=` query that real Jenkins honours, so check changes to queries against a real server. UI conventions are in [docs/ui-contract.md](docs/ui-contract.md), terminology in [GLOSSARY.md](GLOSSARY.md), and remaining work and known gaps in [TODO.md](TODO.md).

## License

MIT
