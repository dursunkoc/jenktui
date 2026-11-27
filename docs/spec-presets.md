# Spec: Parameter Presets

Status: written by the agent on 2026-11-27 from the TODO item "Parameter presets". The owner asked for a spec without a design interview, so the decisions below are the agent's and are open to change. Not yet implemented.

## Why

A developer triggers the same job with the same few sets of values ("staging deploy", "hotfix") many times a day. Today every trigger starts from the job's defaults and the values are retyped. A **Preset** is a named set of parameter values that the Trigger flow can save and apply.

## Decisions

1. **A Preset belongs to a job, not to a folder.** It is stored under the job's key (`jenkins.Job.FullName`, branch names URL-encoded, as in `collections.toml`). A folder has no presets of its own. Reason: the trigger screen never sees the folder, a job may be in many folders, and job-keyed presets need no upkeep when a folder is renamed, moved or deleted. They also work for a "Selection" trigger.
2. **One name spans jobs.** Saving a preset from a trigger of several jobs stores each job's values under the same name. Applying a name to several jobs uses it for every job that has it; the others start from their defaults.
3. **Passwords are never saved.** A `password` parameter is left out on save and stays at its default on apply. This keeps the settled rule that jenktui writes no secrets to disk.
4. **Presets live in their own file**, `presets.toml` beside `collections.toml`, written by the app. `config.toml` stays hand-edited.
5. **Applying a preset goes straight to the summary.** That is the point of a preset: no retyping. The summary already lets the user review every value, edit a job (`e`) and go back.
6. **A rerun ignores presets.** When `Deps.Prefill` is set (rerun from the Run Monitor), the preset step is skipped.

## Behaviour

### Applying

- After the parameters load, if at least one loaded job has a preset and this is not a rerun, the flow shows a **preset step** before any form:

  ```
  Preset
  › No preset (start from the defaults)
    hotfix                1 of 3 jobs
    staging deploy        3 of 3 jobs
  ```

- The first row is always "No preset". The other rows are the preset names of the loaded jobs, merged case-insensitively, sorted by name. With several jobs each row says how many of them have it. With one job the count is not shown.
- `↑`/`↓` (`k`/`j`) move, `Enter` chooses, `Esc` leaves the Trigger flow, `d` deletes the highlighted preset (see below).
- "No preset" continues with the flow exactly as it is today.
- Choosing a preset fills every job that has it with the preset's values and opens the summary:
  - A value for a parameter the job no longer defines is ignored.
  - A value that is no longer one of a choice parameter's choices is ignored, and the parameter keeps the job's default. The summary shows a note naming the job, the parameter and the value.
  - A parameter the preset does not hold (a new parameter, or a password) keeps the job's default.
  - A job that has no preset of that name gets its defaults.
- When a preset was applied and some job has a `password` parameter whose value is empty, the summary shows a note: "Passwords are not saved in presets. Press e to enter them." For a single job the note says to go back (`n` or `Esc`) to the form, which is how its summary is edited today.
- From that summary everything works as it does today: `y` triggers, `e` edits the highlighted job (several jobs), and going back opens the forms, which hold the preset's values. As today, changing a Common Parameter on the way back resets the per-job values, which discards the preset's per-job values too.
- A job with no parameters takes no part: it has no presets, and it is triggered as today.
- With no presets for the loaded jobs, the preset step is not shown and the flow is unchanged.

### Saving

- On the summary, `s` saves the values shown as a preset. The key appears only when presets are wired and at least one job has a parameter that is not a password.
- `s` opens a one-line name prompt under the summary ("Preset name: "), prefilled with the applied preset's name if there is one. `Enter` saves, `Esc` cancels.
- A name is trimmed, must not be empty, and is limited to 40 characters. Names are compared case-insensitively.
- Saving stores, for every job in this trigger that has parameters, all of its final values except passwords, under that name. An existing preset of the same name for one of those jobs is replaced, keeping the spelling just typed. Presets of that name for other jobs are untouched.
- After saving, the summary stays open and shows "Saved preset staging deploy for 3 jobs" (or "for api/deploy" with one job). A write error is shown in its place and the summary stays usable.

### Deleting

- `d` on a preset row asks "Delete preset staging deploy for 3 jobs? y/n" (the count is the loaded jobs that have it). `y` deletes it for those jobs only. `n` or `Esc` cancels.
- When the last preset is deleted, the preset step closes and the flow continues as "No preset".

### Out of scope

- Renaming a preset.
- A screen that lists every preset.
- Removing presets of a job that no longer exists in Jenkins (the file can be edited by hand).
- Presets for a folder as such, and choosing a preset from the Collections tree or the command line.
- Typing to filter the preset step.

### Resolved in planning

The plan ([plan-presets.md](plan-presets.md), "Spec issues") found gaps. Its recommended resolutions are accepted and are part of this spec:

- `Esc` on the first form after the preset step goes back to the step.
- `s` works on a rerun; only the preset step is skipped.
- A job whose only parameters are passwords is left out of a save.
- After a `Load` error the screen itself refuses `s`, with the same message.
- With several jobs, the Common Parameters form shows a preset value only when every job gets the same value. Otherwise the values are per-job overrides.
- A row shows the first job's spelling of a name. M in "N of M jobs" is the jobs whose parameters loaded.
- A hand-edited bool that is not `true` or `false` is treated like a removed choice: the default is kept and a note is shown.
- The trigger screen has no help panel, so the new keys go in the key bar only.
- With one job, the delete question names the job ("for api/deploy"), as the save message does.

## The file

`presets.toml` in the config directory:

```toml
[[preset]]
job = "team/api/deploy"
name = "staging deploy"

[preset.values]
ENV = "staging"
DRY_RUN = "false"
```

- One `[[preset]]` table per job and name. Order: as saved, new ones appended.
- A missing file means no presets. A file that does not parse is an error: the preset step is skipped, the flow continues as today, and a note on the forms and the summary says "Presets are unavailable: <error>". Saving is refused with the same error, so a broken file is never overwritten.
- The file is created with the same permissions and directory handling as `collections.toml`.

## Interfaces (pinned, so the store and the screen can be built in parallel)

In `internal/ui/trigger`:

```go
// PresetStore reads and writes Presets. Values are parameter name -> value.
type PresetStore interface {
	// Load returns job -> preset name -> values for the given jobs. A job
	// without presets is absent from the result.
	Load(jobs []string) (map[string]map[string]map[string]string, error)
	// Save stores values (job -> values) under name, replacing a preset of
	// the same name (letter case ignored) for each of those jobs.
	Save(name string, values map[string]map[string]string) error
	// Delete removes the preset of that name (letter case ignored) for each job.
	Delete(name string, jobs []string) error
}
```

`trigger.Deps` gains `Presets PresetStore`. nil turns the feature off: no preset step and no `s` key.

In `internal/config`:

```go
// PresetFile is the presets.toml at Path. Every call reads the file, and
// Save and Delete write it back, so two jenktui processes do not overwrite
// each other's presets with stale data.
type PresetFile struct{ Path string }

func (p PresetFile) Load(jobs []string) (map[string]map[string]map[string]string, error)
func (p PresetFile) Save(name string, values map[string]map[string]string) error
func (p PresetFile) Delete(name string, jobs []string) error
```

`config.PresetFile` satisfies `trigger.PresetStore` without either package importing the other. `main.go` passes `config.PresetFile{Path: <config dir>/presets.toml}` in `triggerFor` and in the rerun wiring (where `Prefill` makes the screen skip it anyway).

The store never sees parameter types, so the screen is the one that removes passwords before `Save`.

## Screen notes

- `Load` is called inside the existing parameter-loading command, off the UI goroutine. `Save` and `Delete` are small file writes and may run in `Update`, as the Collections screens do.
- The preset step is a new stage. `Busy()` is unchanged.
- Key bar: preset step `enter choose · d delete · esc back`; summary gains `s save preset`.
- The help panel and the README keyboard table list the new keys.

## Docs in the same change

README (Triggering, the keyboard table, the files under Settings), GLOSSARY (a "Preset" entry, and a mention under Trigger), `docs/ui-contract.md` (the Trigger screen section and `trigger.Deps`), `docs/HANDOFF.md` (the `internal/config` and `internal/ui/trigger` rows, and the "no secrets on disk" decision gains "presets skip passwords"), TODO (move the item out of Later), and the wiki pages Triggering (or the page that covers it), Keyboard-Reference and Configuration.

## Tests

- Store (`internal/config`):
  - a missing file loads as empty
  - save then load round-trips
  - save replaces case-insensitively and keeps other jobs' presets of that name
  - delete removes only the named jobs' presets
  - a file that does not parse is an error and is not overwritten by Save
  - Load returns only the jobs asked for
- Screen (`internal/ui/trigger`, with a fake store):
  - no presets: the flow is unchanged
  - a nil store: no step and no `s`
  - one job: the step lists names, "No preset" opens the form with defaults, a preset opens the summary with its values, and going back shows the form holding them
  - several jobs: counts per row; a preset fills the jobs that have it and the rest keep defaults; `e` and back still work
  - stale values: a removed parameter is ignored; a removed choice falls back to the default with a note
  - passwords: never passed to `Save`; the note appears when one is empty after applying
  - save: prompt, prefilled name, trimmed, empty refused, values stored per job, the message; a store error is shown
  - delete: confirm, only the loaded jobs, the step closes after the last one
  - a rerun (`Prefill` set) skips the step
  - a `Load` error: the note, and `s` refuses
- e2e: trigger a parameterized job, save a preset, trigger again, choose it, and check the build got the preset's values and `presets.toml` holds no password.
