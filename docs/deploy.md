# Deployment

Markspace runs as a rootless Podman container that a quadlet manages. A
quadlet is a unit file that Podman turns into a systemd service, so systemd
starts the container at boot, restarts it when it exits, and sends its logs to
the journal. The server runs in the foreground inside the container and stops
cleanly on SIGTERM.

## Units

`deploy/` holds the units.

| File | Purpose |
| --- | --- |
| `markspace.container` | The server, from `ghcr.io/gamemaker1/markspace:latest` |
| `markspace.volume` | The volume `markspace`, mounted at `/data` |
| `markspace.network` | The network `markspace`, shared with plugin containers |
| `wiki.container` | The bookmark wiki plugin, from `ghcr.io/gamemaker1/markspace-wiki:latest` |
| `wiki.volume` | The volume `markspace-wiki`, mounted at `/data` in the plugin |

Install and start the server as the user that runs it:

```sh
mkdir -p ~/.config/containers/systemd
cp deploy/markspace.container deploy/markspace.volume deploy/markspace.network \
  ~/.config/containers/systemd/
systemctl --user daemon-reload
systemctl --user start markspace
loginctl enable-linger "$USER"
```

`loginctl enable-linger` keeps the user's services running after logout and
starts them at boot. `journalctl --user -u markspace` shows the logs.

The server listens on `127.0.0.1:8080` on the host. Open it in a browser. The
setup page creates the first account, which becomes the instance admin. The
admin can also create it from the command line:

```sh
podman exec markspace markspace user add -email you@example.com -admin
```

The image runs as UID 65532 with a read-only root filesystem, no capabilities
and a writable `/tmp`. The health check runs `markspace health`, and
`podman healthcheck run markspace` runs it by hand.

## Configuration

Every setting has a default, so the server starts without a configuration
file. Environment variables override the file. Set them with `Environment=`
lines in `markspace.container`, which lists the common ones as comments.

| Key | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `listen` | `MARKSPACE_LISTEN` | `:8080` | Bind address inside the container |
| `baseUrl` | `MARKSPACE_BASE_URL` | empty | Public root for share, invite and file links. Empty uses the address of each request |
| `dataDir` | `MARKSPACE_DATA_DIR` | `/data` in the image | Spaces, file history, thumbnails and the database |
| `allowSignup` | `MARKSPACE_ALLOW_SIGNUP` | `true` | Open registration. `false` means invite only |
| `secureCookies` | `MARKSPACE_SECURE_COOKIES` | `false` | Marks cookies secure and turns on cross-origin request protection |
| `trustProxy` | `MARKSPACE_TRUST_PROXY` | `false` | Reads the client address from `X-Forwarded-For` |
| `watchFiles` | `MARKSPACE_WATCH_FILES` | `true` | Picks up notes edited outside the application |
| `maxUploadMB` | `MARKSPACE_MAX_UPLOAD_MB` | `1024` | File upload size limit |
| `logLevel` | `MARKSPACE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `ai.fetchPublicPrices` | `MARKSPACE_AI_FETCH_PUBLIC_PRICES` | `true` | Downloads the OpenRouter and LiteLLM price lists once a day |
| `plugins.markspaceUrl` | `MARKSPACE_PLUGINS_URL` | empty | Address plugins use to reach Markspace |

The image reads the configuration file `/data/markspace.yaml` when it exists.
`markspace config example` prints a commented file with every setting. To keep
the file on the host instead, mount a directory and point
`MARKSPACE_CONFIG` at it:

```ini
Volume=%h/.config/markspace:/etc/markspace:ro,Z
Environment=MARKSPACE_CONFIG=/etc/markspace/markspace.yaml
```

`podman exec markspace markspace config check` prints the settings in effect,
with secrets hidden.

API keys in the configuration file expand `${VAR}` from the environment. A
Podman secret keeps a key out of the unit file:

```sh
printf %s 'sk-or-...' | podman secret create openrouter-key -
```

```ini
Secret=openrouter-key,type=env,target=OPENROUTER_API_KEY
```

`docs/models.md` describes providers and routing. Providers can also be added
on `/admin/ai` without a configuration file.

## Reverse Proxy

A reverse proxy on the host terminates TLS and forwards to `127.0.0.1:8080`.
Behind it, set these three variables in `markspace.container`:

```ini
Environment=MARKSPACE_BASE_URL=https://notes.example.com
Environment=MARKSPACE_SECURE_COOKIES=true
Environment=MARKSPACE_TRUST_PROXY=true
```

The proxy must pass `Host` and `X-Forwarded-For`, and must not buffer
responses of `/api/events` and the AI routes, which stream server-sent events.
With Caddy, the site block is:

```
notes.example.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

## Command-Line Tool

The image contains the `markspace` command. `podman exec markspace markspace
<command>` runs it next to the server, and it authenticates to the server with
the built-in "Markspace CLI" API token. `docs/cli.md` lists the commands.

## Backups

The notes are markdown files, and the database holds accounts, shares,
comments and the search index. `markspace backup` writes a gzip tar of the
data directory with a consistent copy of the database:

```sh
podman exec markspace markspace backup > markspace-$(date +%F).tar.gz
```

To restore a backup, stop the service, empty the volume, extract the archive
into it and start the service:

```sh
systemctl --user stop markspace
podman volume rm markspace
podman volume create markspace
podman unshare tar -xzf markspace-2026-09-14.tar.gz -C "$(podman volume inspect markspace --format '{{.Mountpoint}}')"
podman unshare chown -R 65532:65532 "$(podman volume inspect markspace --format '{{.Mountpoint}}')"
systemctl --user start markspace
```

`podman unshare` runs the commands inside the user namespace of rootless
Podman, so the files get the owner that UID 65532 in the container maps to.

## Updates

`AutoUpdate=registry` marks the containers for `podman auto-update`, which
pulls a newer image for the same tag and restarts the service. The systemd
timer runs it once a day:

```sh
systemctl --user enable --now podman-auto-update.timer
```

The tag `latest` follows releases. To stay on one minor version, set
`Image=ghcr.io/gamemaker1/markspace:1.2`. The tag `main` follows the main
branch.

The server migrates its database on start. Take a backup before a release
that changes the major version.

## Plugins

A plugin is a separate HTTP service in its own container on the `markspace`
network. Containers on that network reach each other by container name, so
Markspace reaches the bookmark wiki at `http://bookmark-wiki:8090`, and the
plugin reaches Markspace at `http://markspace:8080`.

1. Set `MARKSPACE_PLUGINS_URL=http://markspace:8080` in `markspace.container`
   and restart the service.
2. Copy `deploy/wiki.container` and `deploy/wiki.volume` to
   `~/.config/containers/systemd/`, run `systemctl --user daemon-reload`, and
   start the service with `systemctl --user start wiki`.
3. Open Admin, then Plugins, then Add plugin, and enter
   `http://bookmark-wiki:8090`. Markspace fetches the manifest, shows the
   permissions the plugin asks for, and installs it with the spaces and roles
   you choose.

When the install call cannot reach the plugin, the page shows the plugin
token and signing secret once. Store them as Podman secrets and uncomment the
`Secret=` lines in `wiki.container`. `docs/plugins.md` specifies the protocol,
and `plugins/wiki/readme.md` describes the plugin.
