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
type Session struct { Config config.Config; Client JenkinsClient; StartCollection string }
type JenkinsClient interface { // implemented by *jenkins.Client
    Ping(ctx context.Context) (string, error)
    ListJobs(ctx context.Context, parent string) ([]jenkins.Job, error)
    JobParameters(ctx context.Context, fullName string) ([]jenkins.ParamDef, error)
}
```

Pass `*Session` to screen constructors. `Client` is nil until login succeeds; the login screen sets it. Credentials live only inside the client, in memory. `StartCollection` is the Collection named on the command line (`jenktui [collection]`): the Menu reads and clears it in its first `Init` and, through `entry.Deps.OpenCollection`, runs a command that finds the Collection (case-insensitive), reads the catalog to flag missing jobs and name Multibranch branches, and answers `entry.Start`. `Start.Screen` (built on the UI goroutine) is pushed over the Menu; `Start.Notice` (unknown name, unreadable Collections file) is shown on the Menu until the next key.

## Router and navigation

`core.NewRouter(session, rootScreen)` is the root `tea.Model`. Navigate by returning a message as a command:

| Message | Effect |
|---|---|
| `Push{Screen}` | push; screen gets `Init()` then a size message |
| `Pop{}` | pop (no-op at root) |
| `Replace{Screen}` | swap top (e.g. splash -> login); if the screen is already lower in the stack (same pointer) the screens above it are dropped and it becomes the top, so it is never on the stack twice |
| `Quit{}` | exit program |

A screen that polls can implement `core.Resumer` (`Resume() tea.Cmd`): after a `Pop` uncovers it, the Router runs the returned command to restart its work, since a screen under another gets no messages. The Run Monitor uses both for rerun.

Use `core.Cmd(core.Push{s})` to build the `tea.Cmd`. `ctrl+c` always quits globally; screens handle `esc` (emit `Pop`) and `q` themselves.

Size: the Router forwards `tea.WindowSizeMsg` to the active screen with Height already reduced by the 2 chrome rows, so a screen can lay out using it directly. A newly entered screen immediately receives the current size. Only the top screen receives messages; async results for a popped or covered screen are dropped, so use `Replace`/`Push` rather than relying on background screens. A screen that must keep working across a `Push` implements `Resumer` (see above); on `Pop` the uncovered screen also gets the current size.

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

Run Monitor `R` (list view, help bar `R refresh`): starts an immediate cycle polling every pollable row (ignores per-row due-ness; still skips failed-to-start, left-queue, cancelled, missing rows), bumps the generation so the in-flight cycle and its scheduled tick are dropped, and shows the header spinner. When the cycle completes the next tick is scheduled as usual (one tick chain alive). `R` is ignored while a details view is open and behind the first-load modal. In the details view `l` opens the Console Log of the highlighted stage or step (the whole build when no stages are known) and `L` that of the whole build, (needs a build number), which fills the body as a bordered modal and replaces the title-row status line while open (error lines, matched by the built-in `errorLine` pattern (`ERROR`, `FAILED`, `FAILURE`, `...Exception`, `Caused by:`, `npm ERR!`, `error:`, `Error:`, `--- FAIL:`), are drawn in the theme Fail colour and `e`/`E` jump to the next and previous one, wrapping, with `error 3/7` in the footer (the count covers the lines kept in the view, up to 10,000); `w` toggles wrapping of long lines, continuation rows have no line number, and scrolling, find and go-to count log lines, not rows (a line that wraps to more rows than the page shows only its first page of rows); `S` saves the log as a command: a whole-build log is fetched again from offset 0 (not the 10,000-line buffer) and cleaned like the view, a stage or step log saves the lines shown, to `<job>-<n>.log` (`/` in the job name becomes `_`) in `Deps.SaveDir` or the working directory (mode 0600, job name decoded first, streamed, a running build is not followed), never overwriting (`-1`, `-2` suffix), and the footer shows the path (relative to the working directory when inside it, else cut from the left) or `save failed: …`; a result for a closed log is dropped; `/` find and `:` go-to open an input that takes every key, `?` included, so the log has no help panel; keys the log does not use are ignored, and `q` still quits); `esc` closes it back to details, and `esc`/`q` leaving the screen drops any in-flight log fetch. The log polls its own tick chain, independent of the runs cycle.

Run Monitor `o` / `y` (list and details view, help bar `o open in browser`, `y copy URL`): `o` opens the highlighted build's URL through `Deps.Sys` and `y` copies it; URLs are built from the configured base URL, never from the `url` Jenkins reports; a row with no build yet uses its job's URL, and a missing job does nothing. Both run as commands and flash the result in the title-row status line (`opened`, `copied`, `sent to terminal clipboard`, or the error). With a nil `Sys` the keys are hidden and ignored; they are also ignored behind the first-load modal (like `R`), and while an overlay is open. In the Console Log `o` opens the build (URL from the configured base URL) and `y` copies the current line (the current match, error or go-to target if there is one, else the last line while following, else the first visible line); both run as commands, show `opened` / `copied` / `sent to terminal clipboard` / the short error in the log footer (the title-row status line is hidden while the log is open), drop a result for a closed log, and are hidden and ignored with a nil `Sys`. Error texts lose a `sysint: ` prefix and are cut to 60 characters. `y` is still "yes" while the confirm prompt is open.

Run Monitor `f` (list, help bar `f next failed`): moves the cursor to the next visible row whose finished build is FAILURE or UNSTABLE, wrapping round; it does nothing when none, in the details view, or behind the first-load modal. `Enter` on such a row opens the details with the marker on the first failed stage (its first failed parallel child when it has them), placed when the stages arrive unless the user has already moved with `↑`/`↓`. `l` on a failed build opens the log scrolled to its end and not following (`G` resumes following); other builds open as before.

Run Monitor test report (`t`, details view of a finished build; help bar `t test report` when the client can serve reports): when the details view opens on a finished build the monitor fetches the test counts once per build and shows `3 failed · 812 passed · 4 skipped` under the details header (nothing when the build has no test report; a transient error is retried at the next open). `t` fetches the full report and opens an overlay (it takes every key, `q` included) listing the failed tests as `class.name`, prefixed with `[matrix cell]` when the report is aggregated, with `more failures than listed` when the report was cut off. `Enter` shows the highlighted test's error and stack trace, scrolled with `↑`/`↓`; `Esc` goes from the trace to the list, and from the list back to the details. A build with no report flashes `no test report for this build`. `t` flashes `loading test report…` until the report arrives. Counts are stored whenever they arrive, even after the monitor was left; a reply that was lost because the monitor was not on top is fetched again at the next visit. Only the full report is dropped when it arrives after the monitor was left or the details moved to another build.

Run Monitor abort (`X`, list and details view; help bar `X abort` only when the selected row is a running build with a number or a queued item with a queue id, and the client can stop builds): `X` opens the confirm prompt `Abort api/deploy #57?` (`Abort api/deploy (queued)?` for a queued item; the job name is shown as in the list). The job and the build number or queue id are captured when the prompt opens. `y` runs `StopBuild` or `CancelQueue`; `n`/`Esc` do nothing. On success the notice `abort requested` is shown and every pollable row is polled at once (as `R`). Errors are flashed short: `not allowed to abort` (403), `already finished or gone` (the build is unknown; the rows are polled too), `no longer queued (started or cancelled)` (the queue item is unknown; polled too), otherwise the short error text, a refused CSRF crumb included. A result that arrives after the monitor was left is dropped; nothing is left waiting for it. `X` does nothing behind the first-load modal.

Run Monitor rerun (`r`, list and details view; help bar `r rerun` only when `Deps.Rerun` is set, the client can read a build's parameters (`BuildContext`) and the selected row has a running or finished build): `r` flashes `loading parameters…` and reads the build's parameters in a command. On success it stops the monitor's polling as when leaving (so late replies are dropped) and pushes the screen `Deps.Rerun(job, params)` returns; `params` holds the build's values by name, without the password ones (they cannot be read back, so the form asks for them). `main.go` wires `Rerun` to the trigger screen with `trigger.Deps.Prefill` set: a parameter the job no longer defines is ignored, a new one keeps its default, a job without parameters gets the plain confirmation. The trigger's `Next` adds the new run to the same monitor and returns it; the Router's `Replace` then drops the form, so the stack is as before. `esc` in the form pops back to the monitor, whose `Resume` restarts polling. A failed read is flashed as `cannot rerun: <short error>`; a reply that arrives after the monitor was left is dropped. `r` does nothing behind the first-load modal.

Run Monitor notifications (`Deps.Notify`, from `notify` in `config.toml`: `failures`, the default when empty, `all` or `off`): when a poll result moves a row from queued or running to finished, and an earlier result of this monitor showed that row queued or running (so the first load never notifies, nor a row that was finished at its first result), the monitor runs a command that calls `Sys.Bell()` and `Sys.Notify("jenktui", "FAILED api/deploy #57")` (`SUCCESS`, `UNSTABLE`, `ABORTED` for the other results; the job name is shown as in the list, without colour). `failures` notifies for FAILURE and UNSTABLE, `all` for every result, `off` never; a nil `Sys` never. A notify error is ignored. Each build notifies once. **Limit:** only the top screen receives poll results and a monitor that is left stops polling, so notifications fire only while that monitor is on show. Notifying from a monitor that is not on show needs a Router change and is not done.
