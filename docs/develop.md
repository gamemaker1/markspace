# Development

This document covers building Markspace, the layout of the source, the test
suites and the release process.

## Requirements

| Tool | Version | Used for |
| --- | --- | --- |
| Go | 1.26 | The server, the Go SDK and the Go tests |
| Node | 22 or newer | `make client` and the layout audit |
| Python | 3.12 or newer | The bookmark wiki plugin, the Python SDK and their tests |
| Podman | 5 or newer | Building and running the images |
| bun | 1.2 or newer | Rebuilding the CodeMirror and pdf.js bundles only |

## Building

```sh
make build
./markspace config example > markspace.yaml
./markspace serve -config markspace.yaml
```

The server listens on port 8080 and writes to `./data`. The build sets the
version from `git describe`. Cross-compiling needs no C toolchain:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o markspace-arm64 ./cmd/markspace
```

`make images` builds the server image from `containerfile` and the plugin
image from `plugins/wiki/containerfile`. Both builds use the repository root
as the context, and `.containerignore` keeps data, tests and caches out of it.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/markspace` | The binary: the server and the management commands |
| `internal/core` | Domain types, errors, permissions and the `Backend` interface |
| `internal/ports` | Interfaces for the file store, the database, the renderer and the AI factory |
| `internal/config` | YAML and environment configuration |
| `internal/store` | Space folders, notes, files, frontmatter, trash, history, file versions and thumbnails |
| `internal/index` | SQLite schema, queries, FTS5 search and embeddings |
| `internal/auth` | argon2id password hashing, token hashing and validation |
| `internal/markdown` | goldmark rendering, wikilinks, sanitizing, diffs and chunking |
| `internal/links` | Link parsing, resolution and rewriting, shared with the browser module `links.js` |
| `internal/anchor` | Comment anchors, and their remapping through edits |
| `internal/extract` | Image scaling, thumbnails, text decoding and PDF text |
| `internal/fetch` | Web page fetching with private-address blocking, and HTML to text |
| `internal/ai` | Provider protocols, the model catalog, routing, prices and prompts |
| `internal/app` | Access control and the application logic |
| `internal/api` | Authentication, middleware and error mapping. `internal/api/testkit` holds handler test helpers |
| `internal/module` | Module registry, event bus, job engine, scheduler and declarative UI. `internal/module/conformance` tests job stores |
| `internal/modules` | Built-in modules, one package each, with their routes |
| `internal/plugin` | The plugin host |
| `internal/wiring` | Assembly of the modules and the plugin host |
| `internal/web` | Server pages, templates, and the static browser client |
| `internal/web/vendor` | Sources of the CodeMirror and pdf.js bundles |
| `sdk` | The Go plugin SDK |
| `sdk/python` | The Python plugin SDK |
| `sdk/example` | An example plugin that counts words |
| `plugins/wiki` | The bookmark wiki plugin |
| `test/server` | Go tests of the whole server over HTTP, and the HTTP fuzz test |
| `test/client` | Unit tests of the browser modules |
| `test/browser` | The browser layout audit |
| `test/vectors` | JSON cases shared by the Go, JavaScript and Python implementations |
| `tools` | Generators for the comment anchor vectors, the icons and the Octicons data |
| `deploy` | Quadlet units |
| `docs` | Design documents |

Go unit and fuzz tests sit next to the code they test, because Go compiles a
test with access to unexported names only inside the package directory. Every
other test lives under `test`, apart from the plugin and SDK tests, which live
in `plugins/wiki/test` and `sdk/python/test`.

## Tests

| Command | Runs |
| --- | --- |
| `make test` | Go unit and integration tests |
| `make race` | The same tests with the race detector |
| `make fuzz` | Every Go fuzz target for `FUZZTIME` each, 30 s by default |
| `make client` | The browser module tests in `test/client` with `node --test` |
| `make python` | The plugin and Python SDK tests |
| `make layout` | The layout audit in `test/browser` against a fresh server |
| `make fixtures` | The layout audit against its fixture pages |
| `make check` | gofmt, go vet, `make test`, `make client` and `make python` |

Seed inputs of the fuzz targets run as regression cases in `make test`. The
Go suite also runs a randomized sequence test that checks that an instance
always keeps an active admin and every space keeps an owner, and a
differential test that compares access decisions against a reference
implementation.

The layout audit needs `playwright-core` and a Chromium build:

```sh
npm install --prefix test/browser
npx --prefix test/browser playwright-core install chromium
```

`test/browser/readme.md` lists the checks and the options of `make layout`.

The race detector and the layout audit use the most memory of the suites. On
a machine with 16 GiB of memory or less, run `go test -p 2`, run `-race` on
one package at a time, and run `make layout` with no other build or test in
parallel.

## Bundles

`internal/web/static/vendor/codemirror.js`, `pdfjs.js` and `pdfworker.js` are
committed bundles. `internal/web/vendor/codemirror/build.sh` and
`internal/web/vendor/pdfjs/build.sh` rebuild them with bun. `tools/octicons`
regenerates `internal/web/static/js/octicons.js` and `internal/web/icons.go`
from an unpacked `@primer/octicons` package through `go generate` in
`internal/web`.

## Continuous Integration

`.github/workflows/ci.yaml` runs on every push and pull request. It checks
formatting and `go vet`, runs the Go tests with the race detector, the browser
module tests, the Python tests on Python 3.12 and 3.14, and the layout audit
fixtures.

A push to `main` or a `v*` tag also builds the images
`ghcr.io/gamemaker1/markspace` and `ghcr.io/gamemaker1/markspace-wiki` for the
amd64 and arm64 architectures of Linux, and pushes them to the GitHub
container registry. The fixture audit does not gate publishing, because its results
depend on the fonts of the runner.

| Event | Image tags |
| --- | --- |
| Push to `main` | `main`, `sha-<commit>` |
| Tag `v1.2.3` | `1.2.3`, `1.2`, `latest`, `sha-<commit>` |

`.github/workflows/layout.yaml` runs the full layout audit every Monday and on
demand, and uploads the report.

## Releases

Run the Release workflow from the Actions tab with a version such as `1.2.3`.
It pushes the tag `v1.2.3`, and the CI workflow then tests the tag, publishes
the images and creates a GitHub release. The workflow pushes the tag with the
`RELEASE_PAT` secret, a personal access token with the `contents` write
permission, because a tag pushed with the default workflow token starts no
other workflow.

The first push of an image creates a private package on GitHub. Set the
visibility of both packages to public in the package settings, or quadlets
need a registry login to pull them.

## Commits

Commit subjects start with the area they change, such as `web: fix the find
bar on short screens` or `ci: publish arm64 images`.
