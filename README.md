English | [Korean](README.ko.md)

# Spine CLI

A Go CLI for creating [Spine](https://github.com/NARUBROWN/spine) applications, checking execution contracts, and exporting API specifications. The installed executable is `spine`.

**Preview channel** | [Published tag `v0.4.4-preview.4`](https://github.com/NARUBROWN/spine-cli/tree/v0.4.4-preview.4)

Use this channel to try features under development and check compatibility.

Other channels: [RC](https://github.com/NARUBROWN/spine-cli/tree/rc) | [Stable](https://github.com/NARUBROWN/spine-cli/tree/main).

## Quick start

Requires Go 1.25.5 or later. Applications using `--validator playground` require Go 1.26.0 or later.

```sh
go install github.com/NARUBROWN/spine-cli/cmd/spine@v0.4.4-preview.5
spine version
spine new todo-api --module github.com/acme/todo-api --channel preview --no-interactive
cd todo-api
spine dev
```

```sh
curl http://localhost:8080/health
```

`/health` returns HTTP 200 and `{"status":"ok"}`. `--channel preview` resolves development branches for the core and platforms. Check the generated `go.mod` for the versions actually selected.

## Common commands

| Command | Purpose |
| --- | --- |
| `spine new` | Create a Spine project |
| `spine dev` | Run the development server and watch for changes |
| `spine g resource` | Generate resource components |
| `spine contract check` | Check the application's registered execution contracts |
| `spine verify` | Check static contracts, formatting, `go vet`, and tests |
| `spine update` | Update the project's Spine and platform dependencies |

For generation commands that modify existing files, review changes with `--dry-run --json`, then pass the reviewed `base_hash` with `--if-match`. Update the CLI itself with the `go install` command above.

## Spine Cloud preview

Sign in with `spine cloud auth login`, then run `spine deploy` in a project containing `spine.toml`. See `spine cloud auth --help` and `spine deploy --help` for supported options.

When an Echo or net/http application has a frontend configured, `spine deploy` builds it and embeds its static assets in a Linux ARM64 executable. Install the frontend's npm dependencies before deployment. The executable, including static assets, must not exceed 32 MiB.

## Root environment variables in React

React CSR integration passes variables from the root `.env` next to `spine.toml` into the frontend build. You do not need a variable prefix or a copy of `.env` in the frontend directory.

```dotenv
# Project root .env
API_URL=/api
APP_NAME=Spine
```

```ts
const apiURL = import.meta.env.API_URL
```

An existing process environment variable with the same name takes precedence. Values are strings; missing variables are `undefined`. Values in `.env` are included in the frontend bundle and are public in the browser.

`spine dev` watches the root `.env` for creation, modification, and deletion, then rebuilds the frontend. Refresh the page to read the new values. `spine run`, `spine build`, and `spine deploy` use build-time values, so rebuild after changing deployment values. Only the root `.env` is read; frontend `.env` and mode-specific `.env.*` files are not read. The application manages server-side `.env` loading.

## Further reading

- [Platform integration](PLATFORMS.md)
- [Version migration guides](migration/)
- [Command contracts for automation](llms.txt)
- [Release operations](release/README.md)
- [MIT license](LICENSE)
