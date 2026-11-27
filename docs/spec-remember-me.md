# Spec: "Remember me" on the login screen

Status: agreed with the owner on 2026-11-27, implemented (uncommitted). `SaveUsername` is optional and defaults to writing `config.toml` in `ConfigDir`, so `main.go` needs no wiring.

## Why

`config.toml` already has an optional `username`, and the login screen prefills the username from it. Nothing in the app writes it, so the user must edit the file by hand. A "Remember me" checkbox lets the login screen save it.

## Behaviour

- The login form has three stops: username, password or API token, and a `[ ] Remember me` checkbox, in that order.
- **No `username` in the config:** the box starts unticked. `space` toggles it.
- **`username` already in the config:** the box shows `[x] Remember me`, greyed out and locked. `space` does nothing and `tab` skips it. A muted hint beside or under it says: "Saved in config.toml. Edit the file to forget it."
- **Saving:** only after a login that succeeds with the box ticked. The trimmed username is written to `username` in `config.toml`. A failed login writes nothing.
- **Locked login:** the username field stays editable, so the user can log in as someone else once. That login never changes the saved username.
- **Forgetting:** there is no key for it. The user deletes the `username` line, and the next launch shows the box unticked.
- **Save failure:** the login still succeeds. The menu shows a one-line notice, "Logged in, but the username could not be saved: <error>". A failed save never blocks a working login.
- The password or API token is never stored. The note under the form stays and gains a second line: "Only your username is saved."

## Keys

| Key | Effect |
|---|---|
| `tab`, `↓` | next stop (a locked checkbox is skipped; wraps) |
| `shift+tab`, `↑` | previous stop |
| `space` | toggle the checkbox, only when it is focused and unlocked |
| `enter` | username: go to the password. Password or checkbox: log in. |
| `esc` | quit |

The key bar adds `space remember me`, shown only while the box is unlocked.

## Changes by file

- `internal/ui/entry/login.go`:
  - Add a `remember` bool and a `locked` flag (true when `sess.Config.Username != ""` at construction). Focus becomes 0..2, and a locked box is skipped.
  - Render the checkbox row and the hint. Add the second note line.
  - On a successful `loginResult`, if the box is ticked and unlocked, call the new dependency and pass a failure to the menu as a notice.
- `internal/ui/entry` `Deps`: add `SaveUsername func(string) error`. It may be nil, in which case the checkbox is hidden.
- `internal/ui/entry/menu.go`: accept and show a one-line notice, if it has none yet.
- `main.go`: wire `SaveUsername` to load the config at its path, set `Username`, and `config.Save` it. Update the in-memory `sess.Config.Username` too.
- `internal/config`: no change, because `Save` already validates and writes the file.
- Docs, in the same change: README (Logging in, Settings), GLOSSARY, `docs/ui-contract.md` (Login screen), `docs/HANDOFF.md` if it lists the login flow, TODO, and the wiki Getting-Started and Keyboard-Reference pages.

## Out of scope

- Storing the password or API token.
- A key or menu item for forgetting the username.
- Remembering other fields, such as the Jenkins URL or the Parent Job.

## Tests

- Unit (`entry_test.go`):
  - the box starts unticked with no `username`, and `space` toggles it
  - with a `username` the box is ticked and locked, `space` does nothing, and `tab` skips it
  - a successful login with the box ticked calls `SaveUsername` with the trimmed name
  - a failed login does not call it
  - an unticked box does not call it
  - a locked login does not call it, even with the username field edited
  - a `SaveUsername` error still reaches the menu, with the notice
  - the password never appears in anything saved
- e2e: log in with the box ticked, check `config.toml` has the `username`, relaunch and see the box locked, then edit the line away and see it unlocked.
