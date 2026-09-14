# Command-Line Tool

The `markspace` binary runs the server and manages a running server from a
shell. Management commands call the JSON API of the running server over HTTP.
No second process opens the SQLite database, so a command is safe while the
server writes to it.

In a Podman container the commands run inside the container with
`podman exec`. The binary there finds the address and the token from the same
configuration and environment as the server, so the commands run without
flags.

```
podman exec markspace markspace status
```

## Commands

| Command | Needs the server | Needs the token | Purpose |
| --- | --- | --- | --- |
| `serve` | no | no | Run the server in the foreground |
| `version` | no | no | Print the version of the binary |
| `config example` | no | no | Print a commented example configuration file |
| `config check` | no | no | Load the configuration and print the effective values |
| `health` | yes | no | Check that the server answers |
| `status` | yes | yes | Print version, uptime, counts, jobs and plugin health |
| `user list` | yes | yes | List accounts |
| `user add` | yes | yes | Create an account |
| `user passwd` | yes | yes | Reset a password and sign out its sessions |
| `user admin` | yes | yes | Grant or revoke admin rights |
| `user disable` | yes | yes | Deactivate an account and sign out its sessions |
| `user enable` | yes | yes | Reactivate an account |
| `reindex` | yes | yes | Reread every markdown file into the index |
| `backup` | yes | yes | Download a backup archive of the data directory |

`markspace help` lists the commands. `markspace help <command>` and
`markspace <command> -h` print the flags of one command.

There is no `user delete` command. Deleting an account hands its spaces to
the acting admin, and the token of the command line belongs to no account.
Use the Users page for that.

There is no per-space reindex. `reindex` rereads every space.

## Exit Codes

| Code | Meaning |
| --- | --- |
| 0 | The command succeeded, or it printed help |
| 1 | The command failed, for example because the server is not reachable, the token was rejected or the server refused the change |
| 2 | The invocation is wrong, for example an unknown flag or a missing `-email` |

Every error is one line on standard error that starts with `markspace:`.

```
markspace: the server is not reachable at http://127.0.0.1:8080 (dial tcp 127.0.0.1:8080: connect: connection refused)
markspace: the server rejected the token at http://127.0.0.1:8080
markspace: -email is required. Run "markspace help user add" for usage.
```

## Running the Server

```
markspace serve [-config path] [-listen addr] [-data dir]
```

`serve` stays in the foreground until it receives SIGINT or SIGTERM, then
stops accepting requests, closes the event and AI streams, and waits up to
20 seconds for open requests. It does not fork and writes no pid file, so
systemd and Podman supervise the process directly.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-config` | `$MARKSPACE_CONFIG`, else `markspace.yaml` | Configuration file. A missing file means the defaults |
| `-listen` | from the configuration | Bind address, overrides `listen` and `MARKSPACE_LISTEN` |
| `-data` | from the configuration | Data directory, overrides `dataDir` and `MARKSPACE_DATA_DIR` |

Running `markspace` with no command also starts the server.

`config example` prints the example file. `config check` takes the same three
flags as `serve`, loads the configuration the same way, and prints the
effective values as YAML. It replaces AI provider API keys and header values
with `[redacted]`. It exits 1 when the file does not parse or fails
validation.

```
podman exec markspace markspace config check
```

## Locating the Server

Commands that need the server accept these flags.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-config` | `$MARKSPACE_CONFIG`, else `markspace.yaml` | Configuration file to read the listen address and data directory from |
| `-data` | from the configuration | Data directory, which holds the token file |
| `-url` | the listen address | Server root, such as `http://127.0.0.1:8080` |
| `-token-file` | `<dataDir>/cli.token` | File that holds the token. `health` has no such flag |

The address is `-url` when given. Otherwise it is the `listen` value of the
configuration, with an empty or wildcard host such as `:8080`, `0.0.0.0` or
`::` replaced by `127.0.0.1`. The commands speak plain HTTP to that address,
so from outside the container pass `-url` with the address of the reverse
proxy or the published port.

The token is `MARKSPACE_TOKEN` when that variable is set. Otherwise it is the
content of `-token-file`, and otherwise the content of `<dataDir>/cli.token`.
Every request except `health` sends it as `Authorization: Bearer <token>`.

## The CLI Token and Rotation

At every start, `serve` makes sure a system API token named `Markspace CLI`
exists and that `<dataDir>/cli.token` holds its plaintext.

- On the first start it issues the token and writes the file.
- When the file is missing or empty, or its token was revoked, it deletes any
  remaining `Markspace CLI` token and issues a new one.
- When the file holds the stored token, nothing changes.

The database stores only the SHA-256 hash of the token, as for every API
token. The server writes the file to a temporary name created with `O_EXCL`
and mode 0600, then renames it over `cli.token`, so no reader sees a partial
token and the file is never readable by other users. If an existing file has
wider permissions, `serve` narrows them to 0600. The server logs one line,
`cli token issued`, with the token id and its display prefix. It never logs
the token.

A system API token belongs to no account. It authenticates as a system actor
with instance admin rights, so it works on a fresh instance before anyone
signs up. Its scope is narrower than a personal token.

- It authenticates on the JSON API under `/api/`, where admin routes accept
  it.
- It is no credential on server pages and on the plugin host API under
  `/api/plugin/v1/`.
- It cannot create API tokens, sign in or own spaces, because it has no
  account.

Admins see the token in the Server tokens table of the Users page at
`/admin/users`, with its prefix, creation time and last use. The same list is
at `GET /api/admin/tokens`. Revoking it there, or with
`DELETE /api/admin/tokens/{id}`, stops the command line at once. The next
start of the server issues a new token and rewrites the file.

To rotate the token by hand, revoke it on the Users page and restart the
service.

```
systemctl --user restart markspace
```

Anyone who can read `cli.token` has admin rights on the JSON API. Keep the
data directory readable only by the user that runs the server. The backup
archive leaves the file out.

## Health

```
markspace health [-config path] [-data dir] [-url url]
```

`health` calls `GET /healthz` without a token and exits 0 when the server
answers `ok` within 2 seconds. It prints one line either way. A quadlet uses
it as the container health check.

```
[Container]
HealthCmd=markspace health
HealthInterval=30s
HealthRetries=3
```

## Status

```
markspace status [-json] [connection flags]
```

`status` prints the version, uptime, listen address, data directory, the
numbers of users, spaces, notes and files, the job count of each status, and
the health of each installed plugin. `-json` prints the body of
`GET /api/admin/status`.

```
$ podman exec markspace markspace status
version        v1.2.3
uptime         26h4m10s
listen         :8080
data dir       /data
users          4
spaces         6
notes          812
files          95
jobs queued    0
jobs running   1
jobs paused    0
jobs done      210
jobs failed    2
jobs canceled  0

PLUGIN         VERSION  HEALTH  FAILURES  LAST CHECK            ERROR
bookmark-wiki  0.3.0    active  0         2026-09-14T09:12:00Z
```

The plugin health is the status the plugin host records: `active`, `down`,
`paused` or `needs_approval`. A plugin is `down` after 3 failed checks of its
`/health` route.

## Accounts

Every `user` command takes the connection flags. The server applies the same
validation, argon2id hashing and last-admin rule as the Users page.

```
markspace user list [-json]
markspace user add -email address [-name name] [-admin] [-password-stdin]
markspace user passwd -email address [-password-stdin]
markspace user admin -email address [-revoke]
markspace user disable -email address
markspace user enable -email address
```

`user list` prints the email, name, role, state and last login of every
account. `-json` prints the account records.

`user add` creates an active account. Without `-name` the name is the part
of the email address before `@`.

`user passwd` sets a new password and signs out every session of the account.

`user add` and `user passwd` read the password from the first line of
standard input with `-password-stdin`. Without it they generate a password of
24 characters from 18 random bytes and print it once, on a line that starts
with `password:`. A password must be at least 8 bytes long.

`user admin` grants admin rights, and `-revoke` removes them. `user disable`
deactivates an account and signs out its sessions. `user enable` reactivates
it. The server refuses to demote or deactivate the last active admin and
answers with HTTP 409.

`podman exec` needs `-i` to pass standard input to the command.

```
podman exec markspace markspace user add -email ada@example.com -name "Ada Lovelace" -admin
printf '%s\n' "$NEW_PASSWORD" | podman exec -i markspace markspace user passwd -email ada@example.com -password-stdin
podman exec markspace markspace user admin -email rex@example.com -revoke
podman exec markspace markspace user list
```

## Reindex

```
markspace reindex [connection flags]
```

`reindex` rereads every markdown file of every space into the index, as the
reindex action of the admin settings page does. It picks up files edited while the
file watcher was off. It has no timeout.

## Backup and Restore

```
markspace backup [-out file] [connection flags]
```

`backup` downloads `GET /api/admin/backup`, a gzip tar archive, to standard
output or to `-out`. With `-out` it writes a temporary file next to the
target and renames it when the archive is complete. It refuses to write the
archive to a terminal.

The server builds the archive as follows.

1. It copies the database with SQLite `VACUUM INTO` to a temporary file in
   the data directory. The copy is one consistent state of the database.
2. It writes that copy as `markspace.db` at the root of the archive.
3. It walks the data directory and adds every directory and regular file,
   such as `spaces/`, `internal/` and plugin data.
4. It deletes the temporary copy.

The walk leaves out the live database and its `-wal`, `-shm` and `-journal`
files, `cli.token`, temporary files of the store (names containing `.tmp-`),
plugin uploads in progress (names starting with `.upload-`), snapshots of
other backups (names starting with `.backup-`), sockets and symbolic links.

The command reads the archive while it copies it and exits 1 when the gzip
or tar stream is incomplete. A server error after the download starts shows
up this way.

The database copy is consistent, but the files are not one snapshot. A note
or file written during the walk can appear in its old or new state, and a
note created after the database copy can be on disk without a database row.
The server rereads the spaces at start and adds such files to the index. For
an exact copy of both, stop the service and archive the data directory.
`VACUUM INTO` also holds the database connection for the duration of the
copy, so requests wait while a large database is copied.

When `dbPath` points outside the data directory, the archive still holds the
copy as `markspace.db`. Move it to `dbPath` before starting the restored
server.

```
podman exec markspace markspace backup > markspace-$(date +%F).tar.gz
```

To restore a backup, follow these steps.

1. Stop the service with `systemctl --user stop markspace`.
2. Extract the archive into an empty data directory. For a rootless Podman
   volume, extract it inside the user namespace so the files keep the owner
   the container sees.

   ```
   dir=$(podman volume inspect markspace --format '{{.Mountpoint}}')
   podman unshare sh -c "rm -rf '$dir'/* '$dir'/.[!.]* && tar -xzf markspace-2026-09-14.tar.gz -C '$dir'"
   ```

3. Start the service with `systemctl --user start markspace`. The server
   finds no `cli.token`, issues a new token and writes the file.

## API Routes

The commands use these routes of the JSON API. Each accepts the CLI token and
an admin session.

| Command | Route |
| --- | --- |
| `health` | `GET /healthz` |
| `status` | `GET /api/admin/status` |
| `user list` | `GET /api/users` |
| `user add` | `POST /api/users` |
| `user passwd`, `user admin`, `user disable`, `user enable` | `GET /api/users?q=` to find the account, then `PATCH /api/users/{id}` |
| `reindex` | `POST /api/reindex` |
| `backup` | `GET /api/admin/backup` |
