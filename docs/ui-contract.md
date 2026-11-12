# UI contract (package `internal/ui/core`)

Screens code against this page. Changes here affect every screen, so keep them small and update this page with them.

## Screen

```go
type Screen interface {
    Init() tea.Cmd
    Update(msg tea.Msg) (Screen, tea.Cmd) // return self (or a new value) as Screen
    View() string                         // body only
    Title() string                        // header text
    Keys() []key.Binding                  // shown in the help bar
}
```

The Router draws the title bar (row 1) and a footer: a full-width rule and, under it, the help bar (last row). `View` renders only the body area, which is 3 rows shorter than the terminal.

```go
type Subtitler interface { Subtitle() string } // optional
```

A screen with a status line (counts, filter state, notices) implements `Subtitler` instead of drawing that line in its body. The Router shows it on the title row after the title, in muted text, cut with `…` to fit the width (dropped when there is no room). Return `""` while there is nothing to count (first load, error, an editor open).

```go
type HelpGroup struct { Title string; Keys []key.Binding }
type FullHelper interface { FullHelp() []HelpGroup } // optional
```

A screen with more keys than fit on one line implements `FullHelper`. Its `Keys()` then returns only the 4 or 5 main keys; the Router ends the help bar with `? all keys`, and `?` opens a panel at the bottom of the body with every group from `FullHelp()` as columns (`esc` or `?` closes it). While the panel is open the Router takes every key, and it closes on any navigation. `FullHelp()` returns nil while a text field has focus (filter box, name input, editor), during a first load or on an error: then there is no `? all keys`, and `?` reaches the screen as typed text. While a list's filter box has focus, `Keys()` returns `list.FilterEditKeys()`.

The help bar never runs past the terminal width: keys that do not fit are dropped from the end and marked `…`; `? all keys` is never dropped.

## Session

```go
type Session struct { Config config.Config; Client JenkinsClient }
type JenkinsClient interface { // implemented by *jenkins.Client
    Ping(ctx context.Context) (string, error)
    ListJobs(ctx context.Context, parent string) ([]jenkins.Job, error)
    JobParameters(ctx context.Context, fullName string) ([]jenkins.ParamDef, error)
}
```

Pass `*Session` to screen constructors. `Client` is nil until login succeeds; the login screen sets it. Credentials live only inside the client, in memory.

## Router and navigation

`core.NewRouter(session, rootScreen)` is the root `tea.Model`. Navigate by returning a message as a command:

| Message | Effect |
|---|---|
| `Push{Screen}` | push; screen gets `Init()` then a size message |
| `Pop{}` | pop (no-op at root) |
| `Replace{Screen}` | swap top (e.g. splash -> login) |
| `Quit{}` | exit program |

Use `core.Cmd(core.Push{s})` to build the `tea.Cmd`. `ctrl+c` always quits globally; screens handle `esc` (emit `Pop`) and `q` themselves.

Size: the Router forwards `tea.WindowSizeMsg` to the active screen with Height already reduced by the 2 chrome rows, so a screen can lay out using it directly. A newly entered screen immediately receives the current size. Only the top screen receives messages; async results for a popped screen are dropped, so use `Replace`/`Push` rather than relying on background screens.

Below 60x16 the Router shows `terminal too small, need at least 60x16` (`core.TooSmall`, `core.MinWidth/MinHeight`).

## Theme

`core.NewTheme()` returns `Theme` (also `router.Theme`). Never hardcode colors. Adaptive colors: `Accent, OK, Warn, Fail, Running, Muted, Border`. Styles: `Title, Selected, MutedTxt, Box, ErrTitle`.

- `th.Badge(core.StatusSuccess|Failed|Running|Queued|Unknown|Missing|Unstable|Aborted)` returns a colored label (Unstable in the warning colour, Aborted muted).
- `th.ErrorView(title, err, hint)` boxed error with a hint line; `th.NoticeView(title, hint)` neutral box. Map domain errors to a `hint` in your own screen; core does not import `internal/jenkins`.

## Job names

`core.RelName(full, parent)` drops the Parent Job's prefix for display. `core.JobName{Name, Branch}` is how a job is shown; `String()` joins a Multibranch project and its branch with `core.BranchSep` (" › "), `Render(th)` draws the separator muted. Do not put a muted separator inside a row that has its own style (cursor, selected): the inner reset would cut that row's highlight short.

## Keymap

Shared bindings: `KeyUp, KeyDown, KeyEnter, KeyBack (esc), KeyFilter (/), KeyQuit (q)`. `?` is reserved for the help panel (`KeyHelp`, `KeyCloseHelp`, handled by the Router) on `FullHelper` screens. Reuse them (`key.Matches(msg, core.KeyBack)`) and return them from `Keys()`; add screen-specific bindings with `key.WithHelp`. Disabled bindings are hidden from the help bar.

## Loading indicators

`core.NewLoading(th)` returns a `core.Loading`, a spinner that ticks only while active. Embed it in a screen.

- `Start() tea.Cmd`: mark active, return the first tick (nil if already active). Batch it with the fetch command.
- `Update(msg) tea.Cmd`: call on `spinner.TickMsg`; returns the next tick, or nil if idle, stale or foreign (so idle screens never redraw).
- `Stop()`: work finished. The indicator stays `Active()` until the minimum visible time (`core.DefaultMinLoading`, 500ms; `SetMin`, clock injectable with `SetNow`) has elapsed since `Start()`; ticks keep the chain alive until the minimum has elapsed AND the work is done, then it ends by itself. A fast refresh therefore still visibly flashes.
- `StopNow()`: finished, hide at once (no minimum). Use it after a blocking `Modal` (nothing to flash behind it) and when leaving a screen.
- `View(text)`: inline `spinner text` for a header/status line.
- `Modal(text, w, h)`: `th.Box` with spinner and text, centered in w x h.

Rules: use `Modal` when the screen has nothing to show yet and is fetching; use `View("refreshing…")` in the header when old data stays visible (never hide or shift rows). No goroutines; ticks are plain commands.

Minimum visible time applies to header spinners: Collection view `R`, Catalog/Picker reload, and the Run Monitor's `R` cycles. The Run Monitor shows the header spinner for the first load and for `R` only, never for automatic polls. Screens use `Stop()` when data was already visible and `StopNow()` when a modal was shown; headers test `load.Active()` so the spinner lingers.

Run Monitor `R` (list view, help bar `R refresh`): starts an immediate cycle polling every pollable row (ignores per-row due-ness; still skips failed-to-start, left-queue, cancelled, missing rows), bumps the generation so the in-flight cycle and its scheduled tick are dropped, and shows the header spinner. When the cycle completes the next tick is scheduled as usual (one tick chain alive). `R` is ignored while a details view is open and behind the first-load modal. In the details view `l` opens the Console Log of the highlighted stage or step (the whole build when no stages are known) and `L` that of the whole build, (needs a build number), which fills the body as a bordered modal and replaces the title-row status line while open (`/` find and `:` go-to open an input that takes every key, `?` included, so the log has no help panel; keys the log does not use are ignored, and `q` still quits); `esc` closes it back to details, and `esc`/`q` leaving the screen drops any in-flight log fetch. The log polls its own tick chain, independent of the runs cycle.
