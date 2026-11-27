# Spec: Collections as one expandable tree

Status: agreed with the owner and implemented on 2026-11-27 (uncommitted). A trigger on a folder with nothing selected covers its whole subtree; `Space` on a folder skips jobs missing from Jenkins; the help panel adds a Folders group.

This replaces the drill-down UI for nested Collections, which was built earlier the same day and is uncommitted. The data model and its decisions stay. Only the screens change.

## Why

The drill-down design has three problems:

- A new folder goes straight to the job picker, so an empty folder cannot be made and then given a sub-folder.
- Folders and jobs are on separate screens: the folder list, and the "opened Collection" screen.
- Folders cannot be expanded and collapsed in place.

## Kept from the folder design (unchanged)

- A folder is a generalised Collection. It holds jobs, sub-folders or both, to any depth.
- The root holds only folders. Empty folders are allowed.
- Names are unique among siblings (case-insensitive) and cannot contain `/`. A path is the names joined with `/`, letter case ignored.
- A job may be in many folders. Subtree views list it once.
- Within a folder, sub-folders come first, then jobs, each in manual order.
- A folder's Run Monitor, Trigger, Overview row and notifications cover its whole subtree.
- `collections.toml` nests with `[[collection.folder]]`. Old flat files load unchanged.
- Run Monitors are keyed by path. Delete and move drop the monitors at or under the path. Rename re-keys them to the new path, because an existing e2e test expects the monitor to follow a rename.
- `jenktui a/b`, `Ctrl+P` (every folder by path) and the Overview (top-level folders with subtree counts) stay as built.
- Config API stays as built: `Get`, `Paths`, `Subtree`, `CountFolders`, `AddIn`, `Rename`, `Delete`, `MoveFolder`, `TransferJobs`, `SetJobs`, `ValidName`.

## The tree screen

One screen replaces both the folder list (`listScreen`) and the opened-Collection screen (`viewScreen`, deleted along with its key bindings):

```
▾ backend           3 folders · 4 jobs
  ▾ develop         1 folder · 3 jobs
    ▸ deep          1 job
      api/build     ● SUCCESS
      api/deploy    ✕ FAILED
  ▸ empty
    own-job         ● SUCCESS
▸ libs              1 job
```

- Rows are folders and jobs, indented by depth. `▸` marks a collapsed folder and `▾` an expanded one.
- Under an expanded folder come its sub-folders, then its own jobs.
- A folder row shows `N folders · M jobs` over its subtree, or `M jobs` when it has no sub-folders, or nothing when empty.
- A job row shows its name (relative to the Parent Job, as `catalog.Row` does) and its status badge. A job no longer in Jenkins shows `MISSING`.
- Status comes from one catalog load when the screen opens, without a blocking modal. Rows show plain names until it arrives. `R` reloads it. A load error shows in the subtitle, and the tree stays usable.
- Every folder starts collapsed. Expansion state is kept in memory while the app runs: leaving and re-entering the screen keeps it. Nothing is saved to disk.
- The title stays `Collections` (with ` · <parent>` when a Parent Job is set). There is no breadcrumb.

## Keys

| Key | On a folder | On a job |
|---|---|---|
| `↑`/`↓`, `k`/`j` | move | move |
| `→`/`l` | expand; if already expanded, go to its first child | — |
| `←`/`h` | collapse; if already collapsed, go to its parent folder | go to its folder |
| `Enter` | go into it (see below) | nothing |
| `Space` | select every job in its subtree; again clears them | toggle the job |
| `n` | new empty folder inside it (asks only for a name) | new empty folder inside the job's folder |
| `N` | new empty top-level folder | same |
| `e` | edit this folder's own jobs in the picker (current ones ticked) | edit the job's folder |
| `r` | rename | — (job names come from Jenkins) |
| `d` | delete with its subtree; confirmation shows counts when it has sub-folders | remove the job from this folder after `y`/`n` ("Remove api/build from develop?"); this also covers missing jobs, so `x` is dropped |
| `M` | move the selected jobs; without a selection, move this folder | move the selected jobs; without a selection, move this job |
| `C` | copy the selected jobs | copy the selected jobs, or this job |
| `Shift+↑/↓` | reorder among sibling folders | reorder among the folder's jobs |
| `t` | trigger (see below) | trigger (see below) |
| `m` | Run Monitor of this folder | Run Monitor of the job's folder |
| `o` | — | open the job in the browser |
| `/` | filter | filter |
| `R` | reload statuses | reload statuses |
| `Esc` | clear the filter, then go back | same |

- After `n`/`N` the new folder is created at once, without the picker. The cursor lands on it, and its parent expands.
- `M` and `C` push the existing folder picker (`destScreen`). Moving a folder into itself or a descendant, or onto a taken name, is refused there. The picker offers the top level only when a folder is being moved.
- Reordering crosses no boundaries: a job never leaves its folder and a folder never changes parent through `Shift+↑/↓`.
- Selection is a set of job keys. A job in two folders is selected in both places.

## Triggering

- `t` triggers the selected jobs. With nothing selected, it triggers the job under the cursor, or the whole subtree of the folder under the cursor.
- Missing jobs are skipped and named in the notice, as now.
- The new builds join the Run Monitor of the deepest folder that contains every triggered job. When the jobs span several top-level folders, they go to a "Selection" monitor kept while the app runs. `Trigger(collection, …)` therefore receives a folder path or the Selection name.
- The Trigger dependency's names map covers the triggered jobs, and its monitor resync uses the target folder's subtree. A Selection monitor uses the triggered jobs only.

## Filter

- `/` filters rows by name. The tree shows every matching folder and job, plus the parent folders that lead to them. Those parent folders are expanded while the filter is on.
- Clearing the filter restores the expansion state from before it.

## Going into a folder (added after the first version)

`Enter` on a folder goes into that folder (`→` expands in place): the screen shows only its sub-folders and then its own jobs, from the top, under a breadcrumb title (`Collections · backend › develop`; a long path collapses its middle to `…`). Expanding and collapsing still work inside. `Esc` clears the filter first, then steps out one level with the cursor on the folder just left, then, at the top, goes back. Inside, `n` and `N` create a folder in the folder gone into, and `e` on an empty folder edits its jobs.

## Out of scope

- Copying folders.
- Saving expansion state to disk.
- Drag-style moves across folders with `Shift+↑/↓`.

## Changes by file

- `internal/ui/collections`:
  - A new tree screen replaces `listScreen` and `viewScreen`. `New` returns it.
  - `picker.go` and `dest.go` are kept.
  - `Breadcrumb` is deleted, and the folder and view tests are rewritten for the tree.
- `internal/ui/collections.Deps` keeps `Trigger`, `Monitor`, `Renamed`, `Deleted`, `Opener`, `JobURL`, `BranchFilter`, `Lister`, `Parent` and `Path`. The expansion state is passed in from `main.go` so it lasts while the app runs, for example `Expanded map[string]bool`, keyed by lower-cased path.
- `main.go`: the trigger target folder (deepest common folder, or Selection) is resolved by the screen and passed as `collection`. `triggerFor` handles the Selection name.
- `widgets.List` probably does not fit the tree, because of indentation, expand state and folder rows. The tree may render its own rows but must keep the list's look: the cursor marker, checkbox column, aligned name, branch and status columns, and the `MISSING` badge.
- Docs in the same change: README (Collections, the keyboard table, Getting started steps 3–4), GLOSSARY (Collection, Run Monitor), `docs/ui-contract.md` (the Folders paragraph, the Busy list, the Collections screen sections), TODO, and the wiki pages Collections and the Run Monitor and Keyboard Reference. The README screenshot `docs/images/collection.png` needs regenerating.

## Tests

- Config: as built.
- Tree screen:
  - expand and collapse
  - `←` on a job goes to its folder
  - `n` makes an empty folder and then a sub-folder in it, without the picker
  - `N` makes a top-level folder
  - `Space` on a folder selects its subtree
  - `t` picks the deepest common folder, and the Selection monitor when the jobs span top-level folders
  - `d` on a job removes it, and `d` on a folder shows counts
  - `M` on a job and on a folder
  - `C`
  - the filter keeps parent folders visible and restores expansion afterwards
  - reorder stays within the folder
  - missing jobs are shown
  - expansion state survives leaving and coming back
- e2e: the existing Collections flows, rewritten for the tree, plus a create-folder-then-sub-folder flow.
