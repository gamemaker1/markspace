# Markspace

> A self-hosted note server that keeps every note as a markdown file.

> [!WARNING] All the code in this repository has been written with the help
> of generative AI.

Markspace stores each note as a markdown file with YAML frontmatter, so the
notes stay readable and editable without the application. Accounts, spaces,
shares, comments and the search index live in one SQLite file next to them.
The server is one Go binary with no cgo dependency. The AI features call the
model servers you configure.

## Features

- A CodeMirror 6 editor with live preview, source and reading modes. Saving
  never reformats the markdown.
- Folders, notes and files in one tree, with wikilinks, backlinks, tags,
  version history, trash and full-text search.
- An offline-first browser client. Reading and editing work without a
  connection, and changes replay when the connection returns. It installs as
  a progressive web app on phones.
- Multiple users, spaces with five roles, per-note sharing, public links and
  comments anchored to passages.
- AI actions in the editor against OpenAI-compatible, Ollama, Anthropic and
  ComfyUI servers, with routing across models, fetched prices, usage records
  and budgets.
- Plugins that run as separate HTTP services, such as the bookmark wiki in
  `plugins/wiki`.

## Usage

Markspace runs as a rootless Podman container from the image
`ghcr.io/gamemaker1/markspace`. Copy the units in `deploy/` to
`~/.config/containers/systemd/` and start the service:

```sh
cp deploy/markspace.container deploy/markspace.volume deploy/markspace.network \
  ~/.config/containers/systemd/
systemctl --user daemon-reload
systemctl --user start markspace
```

The server listens on `127.0.0.1:8080`. The first account created on the
setup page becomes the instance admin. The `markspace` command manages the
instance from the container:

```sh
podman exec markspace markspace status
podman exec markspace markspace user add -email you@example.com -admin
```

[`docs/deploy.md`](docs/deploy.md) covers configuration, reverse proxies,
backups, updates and plugins. [`docs/cli.md`](docs/cli.md) lists every
command.

## Documentation

| Document | Contents |
| --- | --- |
| [`deploy.md`](docs/deploy.md) | Container deployment, configuration, backups and plugins |
| [`cli.md`](docs/cli.md) | The `markspace` command |
| [`develop.md`](docs/develop.md) | Building, the source layout, tests and releases |
| [`design.md`](docs/design.md) | On-disk format, access control, sync feed and package boundaries |
| [`routes.md`](docs/routes.md) | Every HTTP route of the JSON API and the pages |
| [`files.md`](docs/files.md) | Folders, files, links and the file tree |
| [`comments.md`](docs/comments.md) | Comment anchors |
| [`offline.md`](docs/offline.md) | The offline browser client |
| [`ai.md`](docs/ai.md) | AI context, usage records and budgets |
| [`models.md`](docs/models.md) | AI providers, the model catalog, routing and prices |
| [`assistant.md`](docs/assistant.md) | AI in the editor |
| [`plugins.md`](docs/plugins.md) | The plugin protocol |
| [`primer.md`](docs/primer.md) | The interface specification, built on GitHub Primer |
| [`ui.md`](docs/ui.md) | Interface rules that Primer does not cover |

## Development

Building needs Go 1.26. The tests also need Node 22 or newer and Python 3.12
or newer.

```sh
make build    # the markspace binary
make check    # gofmt, go vet, Go, browser module and Python tests
```

[`docs/develop.md`](docs/develop.md) describes the source layout and every
test suite.

## License

Markspace is licensed under the ISC license, a copy of which is in
[`license.md`](license.md). The vendored CodeMirror, pdf.js, Primer CSS,
Octicons and relative-time-element bundles in `internal/web/static/vendor`
keep their own licenses, which the bundles and their directories include.
