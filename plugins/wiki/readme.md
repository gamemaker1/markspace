# Markspace Wiki

> A Markspace plugin that turns bookmarks into summarised source notes and a linked wiki.

The plugin reads bookmarks, saves the readable text of each page as a source
note, summarises each source with a model, and maintains wiki pages about the
people, tools, projects and ideas the sources mention. It changes a note it did
not generate only through a suggestion, which the user accepts or discards. The
plugin id is `bookmark-wiki`. `spec.md` describes the behaviour in full.

## Usage

Build the image from the repository root, or use the published image
`ghcr.io/gamemaker1/markspace-wiki`:

```
podman build -f plugins/wiki/containerfile -t markspace-wiki .
```

Copy `deploy/wiki.container` and `deploy/wiki.volume` to
`~/.config/containers/systemd/`, next to `deploy/markspace.container` and
`deploy/markspace.network`. Run `systemctl --user daemon-reload` and
`systemctl --user start wiki`. In Markspace, open Admin, then Plugins, then Add
plugin, and enter the base URL `http://bookmark-wiki:8090`. The plugin reaches
Markspace at `http://markspace:8080` on the `markspace` network.

Then open the Bookmark wiki page, pick an export file from Chrome, Firefox,
Safari or Edge, or a text file with one URL per line, and confirm the cost
estimate. Karakeep and Linkding imports need their URL and token as plugin
secrets.

## Development

The plugin is a Python 3.12 package in `plugins/wiki/source/wiki` that uses the
standard library and the SDK in `sdk/python/markspace`. Run the tests from the
repository root:

```
python3 -m unittest discover -s plugins/wiki/test
```

The tests use no network. They run the plugin against an in-memory fake host.

## License

ISC
