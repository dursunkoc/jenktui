# Plan: Parameter Presets

Implements [spec-presets.md](spec-presets.md). The spec's decisions and its pinned interfaces (`trigger.PresetStore`, `config.PresetFile`) are fixed. Where this plan settles something the spec leaves open, it is listed under [Spec issues](#spec-issues) and every ticket follows that resolution.

## Machine notes (for every implementer)

- Go is at /opt/homebrew/bin/go (add to PATH): `export PATH=/opt/homebrew/bin:$PATH`.
- macOS sed needs `sed -i ''`.
- zsh needs quoted globs (`--include='*.go'`, `'./...'` is fine).
- Do not commit or push. All tickets share one working tree; touch only the files your ticket owns.
- `TODO.md` already has uncommitted edits by the owner and `docs/spec-presets.md` is untracked: edit, never rewrite or revert, and do not `git checkout`/`git stash` anything.
- Run only your own package's checks; a sibling ticket may leave its package half-edited while you work. Only P4 runs the repo-wide gates.
- `internal/ui/trigger` compiles `internal/config` (through `core.Session`). P1 writes `presets.go` complete and compiling in one write; if P2 sees a compile error in `internal/config`, P1 is mid-edit: wait and rerun, do not fix it.
- `gofmt -l` exits 0 even when it lists files: it must print nothing.

## Tickets at a glance

| Id | Goal | Wave | Depends on |
|---|---|---|---|
| P1 | `config.PresetFile` store | 1 | none |
| P2 | Trigger screen: preset step, apply, save, delete | 1 | none (codes against the pinned interface with a fake store) |
| P3 | Docs: README, GLOSSARY, ui-contract, HANDOFF, TODO, wiki | 1 | none (writes from the spec + this plan; reconciled in P4) |
| P4 | Join: `main.go` wiring, e2e scenario, repo-wide gates, doc string check | 2 | P1, P2, P3 |

```
P1 ──┐
P2 ──┼──> P4
P3 ──┘
```

Why this split: the trigger work stays one ticket because `trigger.go` changes in `Init`, `loaded`, `Keys`, `Update`/`key`, `enterSummary`, `summaryRows` and `view`; two tickets in one package would break each other's `go test`. Docs can run in wave 1 because the user-visible strings and keys are pinned below ([Pinned strings](#pinned-strings-and-keys)); P4 checks them against the code. `main.go` is the only place that builds `trigger.Deps` (lines 134 and 194, checked with grep), so the join is small.

---

## P1: store (`internal/config`)

**Files (exclusive):** create `internal/config/presets.go`, create `internal/config/presets_test.go`.

**Goal:** `PresetFile{Path}` with `Load`, `Save`, `Delete` exactly as pinned in the spec. Do not import `internal/ui/trigger` (and it must not import config).

**Notes**
- File shape: unexported `presetDoc struct { Items []presetEntry \`toml:"preset"\` }`, `presetEntry struct { Job string \`toml:"job"\`; Name string \`toml:"name"\`; Values map[string]string \`toml:"values"\` }`. This produces the `[[preset]]` / `[preset.values]` shape in the spec.
- Read helper modelled on `LoadCollections` (`collections.go` ~355): missing file is an empty doc, other read errors and `toml.Unmarshal` errors are returned as `fmt.Errorf("parse %s: %w", path, err)`.
- Write with `writeFile` (`config.go:117`: `MkdirAll 0700`, temp file `0600`, rename). Same as `SaveCollections`.
- `Load(jobs)`: read; for each entry whose `Job` is in `jobs`, `out[job][entry.Name] = clone(values)`. A job with no entries is absent. If one job has two entries whose names differ only by case (hand edit), the later one wins. Never return a nil inner values map (use an empty map).
- `Save(name, values)`: read first; on any read/parse error return it and do not write (spec: a broken file is never overwritten). For each job in `values`, replace the first entry with that job and `strings.EqualFold` name in place (keep its position, set `Name` to the new spelling, set `Values`), and drop any further case-duplicates for that job; if none, append. Entries for other jobs are untouched. Iterate `values` in sorted job order so appends are deterministic. Do not trim or validate `name` (the screen does); an empty `name` may be refused with an error as a guard.
- `Delete(name, jobs)`: read (error: return, no write); drop entries with `Job` in `jobs` and `EqualFold(Name, name)`; write only if something was dropped.
- Doc comment on `PresetFile` as in the spec. No knowledge of parameter types.

**Tests (spec "Store"):** use `t.TempDir()`.
- missing file loads as empty (non-nil or nil map with len 0, no error).
- Save then Load round-trips (two jobs, values incl. an empty string value); file mode is `0600`.
- Save replaces case-insensitively (`"Staging"` then `"staging deploy"` vs `"STAGING DEPLOY"`): one entry per job, new spelling, other jobs' entries of that name untouched, order kept.
- Delete removes only the named jobs' presets of that name (other names and other jobs stay).
- A file that does not parse: Load errors, Save and Delete error and the file bytes are unchanged afterwards.
- Load returns only the jobs asked for.
- Optional: a hand-written file in the spec's exact TOML loads.

**Verify:** `cd /Users/TTDKOC/Dev/workspaces/g/jenktui && export PATH=/opt/homebrew/bin:$PATH && gofmt -l internal/config && go vet ./internal/config && go test -count=1 ./internal/config`

---

## P2: trigger screen (`internal/ui/trigger`)

**Files (exclusive):** edit `internal/ui/trigger/trigger.go`, edit `internal/ui/trigger/trigger_test.go`, create `internal/ui/trigger/presets.go`, create `internal/ui/trigger/presets_test.go`.

Suggested split inside the package: `presets.go` holds the interface, the preset step (rows, view, keys), sanitising, apply, save and delete; `trigger.go` gets the small hooks. Tests for presets go in `presets_test.go` with a `fakeStore` (records `Save`/`Delete` calls, returns canned `Load` data and errors) and reuse the existing harness in `trigger_test.go` (add a `startWith(t, Deps, jobs...)` variant of `start`, line 89).

### Interface and state
- Add `PresetStore` (verbatim from the spec) and `Deps.Presets PresetStore` (comment: nil turns the feature off).
- New stage `stPreset` (add to the `stage` consts, line 53). `Busy()` (line 123) unchanged.
- New fields on `screen`: `presets map[string]map[string]map[string]string` (from Load), `presetErr error`, `prow int` (cursor), `pdel bool` (delete confirm open), `pmsg string` (error line on the step), `applied string` (display name of the applied preset, "" for none; drives the password note), `saveName string` (prompt prefill: the applied or last saved name), `presetNotes []string` (stale-choice notes for the summary), `naming bool` + `nameIn textinput.Model` (save prompt), `flash string` (save result line), `sawStep bool` (the step was shown).

### Loading (`Init`, line 134; `loaded`, line 250)
- Turn `loadedMsg` into a struct `{res []loadRes; presets map[...]...; presetErr error}` (tests never build it; check with grep).
- In the `load` closure, after `wg.Wait()`: if `deps.Presets != nil && deps.Prefill == nil`, collect the keys with `err == nil && len(defs) > 0` and, if any, call `Load(those)`. Capture `deps.Presets`/`deps.Prefill` outside the closure like `keys, c`.
- In `loaded`: after the `stFailed` return, keep `s.presets`, dropping jobs that are not in `s.jobs` or have no defs. If `presetErr != nil`, append `"Presets are unavailable: " + err` to `s.notes` (banner on forms and summary; `resizeForms` and `summaryRows` already count `s.notes`) and keep `s.presetErr`.
- Split the tail of `loaded` (lines 263-284, the single-job / plan / common logic) into `startFlow() tea.Cmd`. Compute `s.plan` and `s.jobSeq` before the preset decision for several jobs, so apply can use them; keep `startFlow` building the forms. `loaded` then: if `len(s.presetRows()) > 0` → `s.st = stPreset; s.sawStep = true; return s, nil`; else `return s, s.startFlow()`.
- A single job with no parameters goes to `stConfirm` as today and never sees the step (it has no presets after the filter above).

### Preset step
- `presetRows()`: merge names over `s.jobs` order, keyed by `strings.ToLower`; display spelling is the first job's (in `s.jobs` order) spelling; count = loaded jobs having it; sort by lower-cased name. Row 0 is "No preset (start from the defaults)".
- View (in `view()`, add a `case stPreset`): `s.banner()` + title `Preset` + blank + rows, cursor row `th.Selected` with `› `, others two spaces; with `len(s.jobs) > 1` a muted right column `N of M jobs` where M = `len(s.jobs)` (jobs whose parameters loaded, including those without parameters; see Spec issues). Under the rows: the delete question when `pdel`, else `pmsg` in `FailTxt` when set. Scroll if rows exceed the body (rare; a simple window around the cursor is enough).
- Keys in `key()`: handle `stPreset` in the top `switch` and always `return` (otherwise `key()` falls through to `activeForm().Update` at line 590 with a nil form and panics). `up/k`, `down/j` move; `enter` on row 0 → `s.resetFlow(); return s, s.startFlow()`; on a preset row → `return s, s.apply(name)`; `esc` → `Pop`; `d` on a preset row opens `pdel` (ignored on row 0). With `pdel` open: `y/Y` → `deps.Presets.Delete(display, jobsHaving)`; error → `pmsg = "delete failed: "+err`, close confirm; success → remove from `s.presets` (case-insensitive), clamp `prow`, and when no rows are left `s.sawStep = false; s.resetFlow(); return s, s.startFlow()` (so `esc` on the first form pops instead of returning to an empty step). `n/N/esc` close the confirm. Other keys ignored.
- `Keys()` for `stPreset`: `enter choose`, `d delete` (only when a preset row is highlighted), `core.KeyUp`, `core.KeyDown`, `core.KeyBack`; with `pdel` open: `y delete`, `n cancel`.

### Sanitising a preset for one job (`presetValues(job, name) (vals map[string]string, notes []string)`)
- Look up the job's entry by `EqualFold`. For each stored `(n, v)`: skip when the job has no def `n`; skip when the def is `password`; for `choice`, skip when `v` is not in `def.Choices` and add a note `"<s.name(job)>: <n> = <v> is no longer a choice, using the default"`; for `bool`, accept `EqualFold` `true`/`false` and normalise to lower case, otherwise treat like a stale choice (note). Everything else is kept as is.
- This must run before any `params.Options.Prefill`: `params.NewForm` maps a value that is not a choice to the FIRST choice, not the default (`form.go:59`).

### Apply (`apply(name) tea.Cmd`)
Start with `s.resetFlow()` (sets `form`, `commonForm`, `editForm` to nil, `jobForms` to an empty map, `commonVals`, `overrides`, `baseline` to nil, `presetNotes` to nil, `applied` and `saveName` to "", `cursor`, `scroll` to 0; "No preset" calls it too, so an earlier choice leaves nothing behind). Collect notes into `s.presetNotes`, set `s.applied` and `s.saveName` to the row's display name.

Single job (`len(s.jobs) == 1`):
- `s.form = params.NewForm(s.name(job), defs, params.Options{Prefill: vals})`, `s.resizeForms()`, `s.enterSummary()`, return `nil`. `enterSummary` reads `s.form.Values()` (line 429), and `back()` (line 528) reopens the same form, so it holds the preset's values.

Several jobs (the crux; `s.plan`/`s.jobSeq` already built):
1. Target per job: `T[j] = paramsmerge.Resolve(s.plan, nil, nil)[j]` (every job's defaults, all names) overlaid with `presetValues(j, name)`. Jobs without the preset keep their defaults.
2. Common values: if `len(s.plan.Common) > 0`, build the common form with a prefill and take `s.commonVals = s.commonForm.Values()`. Never hand-build `commonVals`: the `reflect.DeepEqual` at line 601 compares it with an untouched form's `Values()`, and any mismatch (a bool spelled `TRUE`, a missing name) wipes `jobForms`/`overrides`/`baseline` the first time the user goes back and presses Enter. Prefill per common parameter `c`: if every job's `T[j][c.Name]` is the same value `v`, prefill `v`; otherwise prefill nothing (the form starts at `c.Default`, or at the empty `(differs)` choice/blank when `c.DefaultsDiffer`). Factor lines 311-315 of `enterCommon` into `buildCommonForm(prefill map[string]string)` used by both. With no common parameters, `s.commonVals = map[string]string{}` as at line 283.
3. Per job, reuse the `stEdit` completion path (lines 606-611): for every job in `s.jobs` with parameters, `f := s.buildJobForm(j, T[j], "")` (this sets `s.baseline[j] = effectiveCommon(j)`), then `s.collectOverrides(j, T[j])`. Store `f` in `s.jobForms[j]` only when `j` is in `s.jobSeq`. `collectOverrides` keeps every per-job value and every common value that differs from the baseline, so `Resolve` reproduces `T[j]` exactly.
4. `s.cursor = 0; s.enterSummary()`; return `nil`. Invariant to assert in tests: `s.final[j] == T[j]` for every job.

Afterwards everything is the existing code: `e` (`editFromSummary`, line 504) rebuilds from `s.final`; `back()` goes to `enterJob(last)` (which reuses `jobForms`) or `enterCommon()` (which now reuses the prefilled `commonForm`, line 311); a changed common value resets per-job state at line 601, as the spec accepts.

### Back to the step
- When `s.sawStep`, `esc` on the first form returns to the preset step instead of popping: `stForm` and `stCommon` at line 585, and `back()` from `stJob` at `pos == 0` with no `commonForm` (line 538, the fall-through to `pop`). Set `s.st = stPreset`, clear `pdel`/`pmsg`. Choosing again runs `resetFlow` first, so nothing leaks between choices. (Spec issue 1.)

### Summary: notes, `s`, prompt
- `enterSummary` (line 426): recompute `s.pwNote` each time: true when `s.applied != ""` and some job has a `password` def whose `s.final` value is empty. It must disappear after the user fills the password via `e` or back.
- Lines drawn between the head and the summary body: each of `s.presetNotes` (muted, prefixed `! ` like `banner`) and the password note: one job `Passwords are not saved in presets. Press n or Esc to go back and enter them.`; several `Passwords are not saved in presets. Press e to enter them.`
- `summaryRows()` (line 627) subtracts `len(s.presetNotes)` and 1 for the password note when shown. `moveCursor` and the pgup/pgdn arithmetic already go through `summaryRows`.
- Footer slot (lines 706-716): when `naming`, the prompt line `Preset name: ` + `nameIn.View()`; else when `flash != ""`, the flash (muted for success, `FailTxt` for an error); else today's hint, which is hard-coded text: add `s save preset` to it when `canSave()`, as well as to `Keys()`. Keep the scroll suffix. The footer stays one line, so no extra row is needed.
- `canSave()`: `s.deps.Presets != nil` and some job in `s.jobs` has a def whose type is not `password`. True on a rerun too (Spec issue 2).
- `key()` `stSummary` branch (line 559): clear `flash` on any key first. When `naming`, every key goes to the prompt before the existing switch (so `y`, `n`, `e`, `j`, `k` are typed): `enter` → save; `esc` → close prompt; otherwise `nameIn.Update(k)` and return its command. `s` (when `canSave`) → `naming = true`, `nameIn` = new `textinput` with `CharLimit 40`, `Prompt ""`, placeholder and completion styles from `th.Placeholder` (ui-contract rule), value `s.saveName`, `CursorEnd`, return `nameIn.Focus()`. `Keys()` while naming: `enter save`, `esc cancel` only.
- Forward non-key messages to `nameIn` while naming: `forward` (line 205) returns `nil` on the summary today; add the prompt there so the cursor blink works.

### Save
- On `enter` in the prompt: `name := strings.TrimSpace(value)`; empty → `flash = "preset name is required"`, keep the prompt open. If `s.presetErr != nil` → `flash = "Presets are unavailable: "+err`, close prompt, no `Save` call (Spec issue 4).
- Values: for every job in `s.jobs` with defs, `s.final[j]` minus `password` defs; skip a job whose map ends up empty (Spec issue 3). Call `deps.Presets.Save(name, values)` in `Update` (spec: small file write).
- Error → `flash = "save failed: "+err` (`FailTxt`), summary stays usable. Success → `flash = "Saved preset <name> for <N> jobs"`, or `"Saved preset <name> for <s.name(job)>"` when exactly one job was saved; set `s.saveName = name` (not `applied`: no preset was applied, so no password note); update `s.presets` in memory (replace case-insensitively per job) so a later return to the step lists it. Close the prompt.

### Tests (spec "Screen"; fake store; window 100x40 as in `start`)
- No presets (`Load` returns empty): single job goes straight to the form; several go to Common Parameters; existing tests pass unchanged with `Presets: nil`.
- Nil store: no step even when the fake would have data; summary `Keys()` has no `s`; pressing `s` does nothing.
- One job: step lists names without counts and "No preset"; "No preset" opens the form with defaults; a preset opens the summary with its values (`ENV = prod`); `n` shows the form holding them; `esc` on that form returns to the step.
- Several jobs: rows show `2 of 3 jobs`; a preset fills the jobs that have it, the rest keep defaults; assert `final` equals the expected per-job map (this is the invariant above), including a `(differs)` common parameter where the preset values disagree and one job without a per-job step; `e` on that job shows the preset value; back walks `jobSeq` forms holding the values; going back to the common form and pressing Enter without changes keeps every per-job value (guards the line-601 reset); changing a common value then resets per-job values.
- Stale values: a removed parameter is ignored; a removed choice falls back to the job's default (not the first choice) and the summary shows the note naming job, parameter and value.
- Passwords: the `Save` call never contains a password parameter; after applying, the password note shows when a password is empty and is gone after `e` fills it (several jobs) / after back and re-submit (one job).
- Summary hint: existing assertions on `e edit job` (trigger_test.go lines 274, 333) keep passing; the multi-job hint becomes `y/enter trigger, e edit job, s save preset, up/down select job, n/esc back` (no `e edit job` when `canEdit` is false). `e2e_test.go` does not assert the hint (checked with grep).
- Save: `s` opens the prompt prefilled with the applied name; typed name is trimmed; empty is refused; 41st character is not accepted; the fake got one `Save` with values per job; the flash text for 1 and 3 jobs; a store error shows `save failed: …` and `y` still triggers.
- Delete: `d` asks `Delete preset staging deploy for 2 jobs? y/n`; `n` cancels; `y` calls `Delete(name, only the loaded jobs having it)`; deleting the last preset continues as "No preset" (form/common form shown).
- Rerun (`Prefill` set): `Load` is never called (fake counts calls), no step.
- `Load` error: no step, the forms and the summary show `Presets are unavailable: <err>`, `s` shows that error and `Save` is not called.

**Verify:** `cd /Users/TTDKOC/Dev/workspaces/g/jenktui && export PATH=/opt/homebrew/bin:$PATH && gofmt -l internal/ui/trigger && go vet ./internal/ui/trigger && go test -count=1 ./internal/ui/trigger`

---

## P3: docs

**Files (exclusive):** `README.md`, `GLOSSARY.md`, `docs/ui-contract.md`, `docs/HANDOFF.md`, `TODO.md`, `../jenktui.wiki/Collections-and-the-Run-Monitor.md`, `../jenktui.wiki/Keyboard-Reference.md`, `../jenktui.wiki/Configuration.md`.

Write from the spec, this plan's [Pinned strings](#pinned-strings-and-keys) and the [Spec issues](#spec-issues) resolutions. Plain, short sentences in the existing voice. No commits in either repo.

- **README:** Features bullet "Trigger" (line 27) and the Usage "Trigger" paragraph (line 133): presets in one or two sentences (job-keyed, one name across jobs, applying goes to the summary, passwords not saved, rerun ignores them). Keys table (around line 170): a "Trigger preset step" row and `s` on the summary rows (one job and several). Configuration (line 121): one sentence on `presets.toml`, written by the app, beside `collections.toml`. Do not claim presets are chosen from the Job Catalog (it has no trigger; Spec issue 6).
- **GLOSSARY:** new "Preset" entry (terms only, no implementation); add a mention under "Trigger" (line 22).
- **docs/ui-contract.md:** it has no Trigger section today. Add a short `## Trigger` section (after Run Monitor) describing `trigger.Deps` (`Client`, `Names`, `Parent`, `Prefill`, `Presets`, `Next`), the stages incl. the preset step, `Busy` during loading/posting only, the key bar of the step and the summary (`s save preset`), and that the screen is not a `FullHelper`. Mention `config.PresetFile` satisfies `trigger.PresetStore` without imports between them.
- **docs/HANDOFF.md:** Layout rows `internal/config` (line 23: add `presets.toml`, app-written) and `internal/ui/params`, `internal/ui/trigger` (line 36: preset step); settled decision "Credentials are never stored" (line 45) gains "presets skip passwords"; Current state: one line that presets are implemented, not released, not checked on a real Jenkins.
- **TODO.md:** move "Parameter presets" (line 135) out of "Later" into the done list in the style of the existing `[x]` items (moved from Later), pointing at `docs/spec-presets.md`. Edit only that item; the file has the owner's uncommitted edits.
- **Wiki** (`../jenktui.wiki`): `Collections-and-the-Run-Monitor.md` "Triggering" section (line 41): a "Presets" subsection (choose, save with `s`, delete with `d`, passwords, rerun). `Keyboard-Reference.md`: a Trigger section with the step and summary keys. `Configuration.md`: a `presets.toml` section after `collections.toml` (line 33) with the spec's example.

**Verify:** `cd /Users/TTDKOC/Dev/workspaces/g/jenktui && git diff --stat -- README.md GLOSSARY.md docs TODO.md && git -C ../jenktui.wiki diff --stat`; read each changed paragraph once against the pinned strings.

---

## P4: join (wave 2)

**Files (exclusive):** edit `main.go`, create `e2e_presets_test.go`. May also fix doc strings in P3's files if they disagree with the code (P3 is finished by then).

**Notes**
- `main.go`: next to `collectionsPath` (line 109) add `presetsPath := filepath.Join(dir, "presets.toml")`; pass `Presets: config.PresetFile{Path: presetsPath}` in `triggerFor` (line 194) and in the rerun wiring (line 134).
- e2e in a new file so `e2e_test.go` stays untouched. Reuse `paramJob`, `fillDeployForm`, `collectionsTOML`, `secretValue`, `vals` from `e2e_test.go` and the helpers in `e2e_helpers_test.go`. Do not use `triggerFor` (it fails unless the job was triggered exactly once); read `f.Triggers()` directly.
- Scenario (fires twice, as the spec words it): `newApp` with `paramJob` and Collection `Ops` → `loginOK`, `openCollections`, `openCollection("Ops")`, `t` (no step: no file yet), `fillDeployForm`, on the summary `s`, `typeText("staging deploy")`, `enter`, expect `Saved preset staging deploy for team/api/deploy`. Read `h.path("presets.toml")`: holds `ENV = "prod"`, has no `API_KEY` and not `secretValue`. `y` → expect title `Run Monitor: Ops`; `esc` → `Collections` (as in `TestMonitorKeptAcrossLeavingTriggerAndRename`). `t` again → expect `Preset`, `staging deploy`; `down`, `enter` → expect `Ready to trigger 1 job(s)`, `  ENV = prod`, the password note; `y`. Assert the LAST entry of `f.Triggers()` for the job has `ENV=prod, DRY_RUN=true, REGION=us, API_KEY="", NOTES=line-2` and `presets.toml` still holds no secret.
- Check P3's README/wiki/ui-contract strings against the implemented keys and messages; fix any drift.

**Verify (repo-wide, this ticket only):**
```
cd /Users/TTDKOC/Dev/workspaces/g/jenktui && export PATH=/opt/homebrew/bin:$PATH
gofmt -l . && go vet ./... && go test -count=1 ./... && go test -race -count=1 ./internal/ui/trigger ./internal/config .
```

---

## Pinned strings and keys

Used by P2 (code), P3 (docs) and P4 (e2e). Change them only in all three.

- Step title `Preset`; first row `No preset (start from the defaults)`; count column `N of M jobs` (several jobs only).
- Step keys: `↑`/`↓` (`k`/`j`), `enter choose`, `d delete`, `esc back`. Delete question: `Delete preset <name> for <N> jobs? y/n` (`for <job>` when N is 1).
- Summary key `s save preset`; prompt `Preset name: `; messages `Saved preset <name> for <N> jobs` / `for <job>`, `preset name is required`, `save failed: <err>`, `Presets are unavailable: <err>`.
- Notes: `<job>: <PARAM> = <value> is no longer a choice, using the default`; `Passwords are not saved in presets. Press e to enter them.` (several jobs) / `Passwords are not saved in presets. Press n or Esc to go back and enter them.` (one job).

## Spec issues

1. **Esc on the first form after the step.** The spec does not say. Resolution: when the step was shown, `esc` on the first form (single-job form, Common Parameters, or the first per-job step when there are no common parameters) returns to the step; choosing again starts clean. Without the step, `esc` pops as today.
2. **`s` on a rerun.** Spec: "a rerun ignores presets" but `s` is only tied to a non-nil store. Resolution: on a rerun `Load` is not called and there is no step, but `s` works (saving a rerun's values is useful; the store's own read refuses a broken file).
3. **A job whose only parameters are passwords** would save an empty preset that then shows in the step. Resolution: such a job is left out of the save; the count in the message is the jobs actually saved.
4. **Who refuses `s` after a `Load` error.** Resolution: the screen remembers the error and refuses without calling `Save` (with the same text); `PresetFile.Save` also refuses a broken file on its own, which covers reruns where `Load` is not called.
5. **"The forms hold the preset's values" with several jobs.** The Common Parameters form shows a preset value only when every job ends up with the same value; otherwise it shows the shared default or `(differs)`, and the jobs' values live in their overrides (visible in their per-job form or with `e`). A job with only common parameters has no form of its own; its preset values are on the summary and reachable with `e`.
6. **"They also work from the Job Catalog".** The Job Catalog has no trigger today (only the Collections tree and the Run Monitor rerun build `trigger.Deps`). Presets work wherever the trigger screen is used; docs must not promise a Catalog trigger.
7. **Name spelling across jobs and the M in "N of M".** Resolution: a row shows the spelling of the first loaded job (in trigger order) that has it; M is the number of jobs whose parameters loaded (jobs without parameters included, since they take part in the trigger).
8. **Stale bool values** (hand-edited `"yes"`) are not covered. Resolution: treated like a stale choice: default kept, note shown.
9. **"The help panel lists the new keys".** The trigger screen is not a `core.FullHelper`, so there is no `?` panel; the keys go in the help bar (`Keys()`) and the hard-coded summary footer hint. The multi-job summary bar becomes long; the Router drops keys from the end with `…`, so put `s` right after `e` in `Keys()`.
10. **ui-contract "Trigger screen section"** does not exist; P3 adds one.
11. **Delete question with one job.** The spec gives only `for 3 jobs`; resolution: `for <job>` when one loaded job has it, matching the save message.

## Risks and tricky cases in the existing flow

- **Line-601 reset.** `commonVals` must equal an untouched `commonForm.Values()`; take it from the form (P2 apply step 2). Otherwise going back over the common form wipes the applied per-job values.
- **First-choice fallback** in `params.NewForm` (`form.go:59`): a stale choice prefilled would silently become choice 0. Sanitise before every `Prefill`.
- **`(differs)` common parameters:** `Resolve` treats an empty common value as "not set" (`paramsmerge.go:142`), so leaving the common form blank and carrying the preset value in overrides is correct; prefilling a disagreeing value would push one job's value onto all.
- **Jobs with no per-job step** (only common parameters): no entry in `jobForms`; their preset values exist only in `overrides`. `collectOverrides` must still run for them, after `baseline[j]` is set (`buildJobForm` sets it; call it even if the form is thrown away).
- **Jobs with no parameters** in a multi-job trigger: `final[j]` is empty, `canEdit` is false, they never appear in presets or saves.
- **nil form dereference:** `key()` falls through to `activeForm().Update` (line 590); the step, the delete confirm and the name prompt must return before that.
- **Typing in the prompt** must not trigger: `y`/`enter`/`n` are summary keys, so the prompt intercepts all keys while open.
- **Stale state between choices:** returning to the step and choosing again must reset `form`, `commonForm`, `jobForms`, `overrides`, `baseline`, `commonVals`, `presetNotes`.
- **`summaryRows` height:** every extra summary line (notes) must be subtracted or the last rows fall off screen at small heights.
- **Load error and `stFailed`:** add the preset note after the `stFailed` check, so "Cannot trigger" lists only the jobs' errors.
- **Concurrency:** `Save`/`Delete` read the file each time (pinned), so two jenktui processes do not lose each other's presets; the screen's in-memory copy may be stale, which only affects the step's rows.
