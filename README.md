# SSDOV — SSH Downloader

SSDOV is a small SSH application for browsing, previewing, downloading, and optionally uploading files from a NAS or Linux server.

It runs as its own SSH server on a separate port, so your normal OpenSSH server on port `22` keeps working normally.

Default SSDOV port: `2222`

## Features

- SSH-based file browser and downloader
- Modern Bubble Tea + Lip Gloss terminal UI
- File/folder navigation over SSH
- File preview in the TUI
- Download command hints for selected files
- Direct non-interactive commands for scripts
- Optional legacy SCP upload support with `scp -O`
- Password authentication and/or public-key authentication
- Path traversal protection: users stay inside the configured download root
- Optional first-run setup menu
- Optional systemd user service installation from the setup menu
- Does not replace or modify normal OpenSSH on port `22`

## Screenshot-style preview

```text
SSDOV ⠋
root /

› 📁  docs/                              folder
  📄  hello.txt                            24 B

Run locally: scp -O -P 2222 '/home/me/photo.jpg' 'download@server:photo.jpg'
[ Enter Open/View ] [ D Download ] [ U Upload ] [ B Back ] [ R Refresh ] [ Q Quit ]
```

## Requirements

- Go, matching the version in `go.mod`
- An SSH client on the client machine
- `scp` with legacy SCP mode support for uploads, usually enabled with `scp -O`
- Linux + systemd only if you want the systemd user service option

## Build

```bash
go build -o ssdov .
```

Run tests:

```bash
go test ./...
```

## Quick start: setup menu

Running SSDOV with no flags opens the interactive server setup menu:

```bash
./ssdov
```

The setup menu lets you configure:

| Setup field | Meaning |
|---|---|
| Port | SSH port SSDOV listens on, for example `2222` |
| Download folder | Folder clients can browse and download from |
| Upload folder | Optional folder for SCP uploads; leave off to disable uploads |
| SSH username | Username clients must use, default `download` |
| Server password | Password for SSH login |
| Start on boot | Install a systemd user service |

Setup menu keys:

| Key | Action |
|---|---|
| `↑` / `↓` | Move between fields |
| `Tab` / `Shift+Tab` | Move between fields |
| `Enter` | Move to next field, toggle service field, or start server on confirm |
| `Space` | Toggle `Start on boot` when that field is selected |
| `Backspace` | Edit current field |
| `Esc` / `Ctrl+C` | Cancel |
| `Y` | Create a missing folder when prompted |
| `N` | Do not create the missing folder; return to editing |

Notes:

- The setup menu requires a password.
- If `Start on boot` is enabled, SSDOV writes a user systemd service named `ssdov.service`.
- If a password is used with the generated service, it is written into the user service as an environment variable. Protect your user account and do not share that service file.

## Manual run examples

### Password auth

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -root /srv/downloads -user download
```

### Public-key auth

```bash
export SSHDOWN_AUTHORIZED_KEYS=/home/youruser/.ssh/authorized_keys
./ssdov -addr :2222 -root /srv/downloads -user download
```

### Password auth + uploads

Start the server with an upload directory:

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -root /srv/downloads -user download -upload /srv/uploads
```

Then upload from a client with legacy SCP mode:

```bash
scp -O -P 2222 /local/path/photo.jpg download@server-ip:photo.jpg
```

### Same folder for downloads and uploads

If you pass `-upload` and do not explicitly pass `-root`, SSDOV uses the upload directory as the download root too:

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -user download -upload ~/Downloads
```

### Local testing without auth

For local testing only:

```bash
export SSHDOWN_INSECURE_ALLOW_ANY=1
./ssdov -addr :2222 -root ./testdata -user download
```

Do not use `SSHDOWN_INSECURE_ALLOW_ANY=1` on a real server.

## All server flags

| Flag | Env var | Default | Meaning |
|---|---|---:|---|
| `-addr` | `SSHDOWN_ADDR` | `:2222` | Listen address or port. Examples: `2222`, `:2222`, `127.0.0.1:2222`, `0.0.0.0:2222` |
| `-root` | `SSHDOWN_ROOT` | `.` | Download root directory. Clients cannot browse outside it |
| `-host-key` | `SSHDOWN_HOST_KEY` | `./sshdown_host_ed25519` | SSH host key path. Wish creates it if missing |
| `-user` | `SSHDOWN_USER` | `download` | Allowed SSH username |
| `-upload` | `SSHDOWN_UPLOAD_DIR` | disabled | Existing server directory where SCP uploads are saved |

Upload can be disabled with any of these values:

```text
"", off, none, disabled, no
```

## All environment variables

| Env var | Used for |
|---|---|
| `SSHDOWN_PASSWORD` | Enables password auth. The client must log in as the configured `-user` and use this password |
| `SSHDOWN_AUTHORIZED_KEYS` | Enables public-key auth using an authorized keys file |
| `SSHDOWN_INSECURE_ALLOW_ANY=1` | Allows any password for the configured user. Local testing only |
| `SSHDOWN_ADDR` | Default value for `-addr` |
| `SSHDOWN_ROOT` | Default value for `-root` |
| `SSHDOWN_HOST_KEY` | Default value for `-host-key` |
| `SSHDOWN_USER` | Default value for `-user` |
| `SSHDOWN_UPLOAD_DIR` | Default value for `-upload` |

SSDOV refuses to start unless at least one auth method is configured:

- `SSHDOWN_PASSWORD`
- `SSHDOWN_AUTHORIZED_KEYS`
- `SSHDOWN_INSECURE_ALLOW_ANY=1`

## Use from a client

### Open the interactive TUI

```bash
ssh -p 2222 download@server-ip
```

### TUI keys

| Key | Action |
|---|---|
| `↑` / `k` | Move selection up |
| `↓` / `j` | Move selection down |
| `PgUp` | Move up by 10 rows |
| `PgDown` | Move down by 10 rows |
| `Enter` / `Right` / `l` | Open folder or preview file |
| `Backspace` / `Left` / `h` / `b` | Go to parent folder |
| `d` | Show a download command for the selected file |
| `u` | Start the upload helper, only if uploads are enabled |
| `r` | Refresh current folder |
| `q` / `Esc` / `Ctrl+C` | Quit |

### Upload helper keys

When the TUI upload helper is open:

| Key | Action |
|---|---|
| Paste or drag a local path | Fill the local file path |
| `Enter` | Accept the current input and continue |
| `Backspace` | Edit input |
| `Esc` / `Ctrl+C` | Cancel upload helper |

The upload helper shows the exact `scp -O` command to run locally. Some terminals may also copy the command automatically using OSC52 clipboard support.

## Direct commands

Direct commands are useful for scripts and automation:

```bash
ssh -p 2222 download@server-ip '<command>'
```

| Command | Meaning |
|---|---|
| `help` | Show help |
| `?` | Same as `help` |
| `ls` | List the download root |
| `ls <path>` | List a folder under the download root |
| `stat <path>` | Show whether the path is a file or directory, its size, and its path |
| `download <file>` | Stream a file to stdout |
| `cat <file>` | Same as `download` |
| `get <file>` | Same as `download` |
| `exit` | Leave the basic interactive shell |
| `quit` | Same as `exit` in the basic interactive shell |

Examples:

```bash
ssh -p 2222 download@server-ip ls
ssh -p 2222 download@server-ip 'ls docs'
ssh -p 2222 download@server-ip 'stat docs/readme.md'
ssh -p 2222 download@server-ip 'download docs/readme.md' > readme.md
ssh -p 2222 download@server-ip 'cat docs/readme.md' > readme.md
ssh -p 2222 download@server-ip 'get docs/readme.md' > readme.md
```

Direct download mode writes the raw file bytes to stdout, so redirects and scripts work naturally.

## Uploads with SCP

Uploads are disabled by default. Start the server with `-upload <server-directory>` or set `SSHDOWN_UPLOAD_DIR` to enable uploads.

```bash
scp -O -P 2222 /local/path/photo.jpg download@server-ip:photo.jpg
```

Upload rules:

- Only legacy SCP upload mode is supported, so use `scp -O`.
- Uploads must be single files.
- Recursive directory upload is not supported.
- The server-side destination must be a plain filename, not a path.
- SSDOV always saves uploads inside the configured upload directory.
- Existing files are not overwritten.
- SCP preserve-time messages are accepted but timestamps are ignored.

Good upload destinations:

```bash
scp -O -P 2222 photo.jpg download@server-ip:photo.jpg
scp -O -P 2222 archive.zip download@server-ip:backup.zip
```

Rejected upload destinations:

```bash
scp -O -P 2222 photo.jpg download@server-ip:../photo.jpg
scp -O -P 2222 photo.jpg download@server-ip:subfolder/photo.jpg
scp -O -r -P 2222 myfolder download@server-ip:myfolder
```

## Paths and security behavior

Download paths are always resolved under `-root`.

Allowed:

```text
docs/readme.md
photos/image.jpg
.
```

Rejected:

```text
/etc/passwd
../secret.txt
docs/../../secret.txt
```

Important security notes:

- SSDOV only serves downloads from files under `-root`.
- Absolute paths and `..` traversal are rejected for download paths.
- SCP uploads are disabled unless `-upload <directory>` is set.
- SCP upload destinations must be plain filenames.
- The server always saves uploads inside the configured upload directory.
- Existing uploaded files are not overwritten.
- Set `SSHDOWN_PASSWORD` or `SSHDOWN_AUTHORIZED_KEYS` before starting.
- `SSHDOWN_INSECURE_ALLOW_ANY=1` is only for local testing.
- Do not commit generated host keys, passwords, private keys, or real server configuration to GitHub.

## Systemd user service

### Option 1: install from the setup menu

Run SSDOV with no flags:

```bash
./ssdov
```

Set `Start on boot` to `enabled`, then start the server. SSDOV writes and enables a user service named:

```text
~/.config/systemd/user/ssdov.service
```

The generated service uses the current binary path and the options you selected.

### Option 2: install from the example file

Edit `ssh-downloader.service.example`, then copy it to your user systemd folder:

```bash
mkdir -p ~/.config/systemd/user
cp ssh-downloader.service.example ~/.config/systemd/user/ssdov.service
systemctl --user daemon-reload
systemctl --user enable --now ssdov.service
```

Check logs:

```bash
journalctl --user -u ssdov.service -f
```

Useful systemd commands:

```bash
systemctl --user status ssdov.service
systemctl --user restart ssdov.service
systemctl --user stop ssdov.service
systemctl --user disable ssdov.service
```

## Common client commands

Replace `server-ip` with your server IP or hostname.

```bash
# Open the TUI
ssh -p 2222 download@server-ip

# List root
ssh -p 2222 download@server-ip ls

# List a folder
ssh -p 2222 download@server-ip 'ls docs'

# Download a file
ssh -p 2222 download@server-ip 'download docs/readme.md' > readme.md

# Upload a file when uploads are enabled
scp -O -P 2222 photo.jpg download@server-ip:photo.jpg
```

## Troubleshooting

### `refusing to start without auth`

Set one of these before starting:

```bash
export SSHDOWN_PASSWORD='change-this-password'
# or
export SSHDOWN_AUTHORIZED_KEYS=/home/youruser/.ssh/authorized_keys
# or local testing only
export SSHDOWN_INSECURE_ALLOW_ANY=1
```

### `Download folder must exist`

Create the folder first:

```bash
mkdir -p /srv/downloads
```

Then start SSDOV:

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -root /srv/downloads
```

### Upload does not work

Make sure the server was started with `-upload` and that the client uses `scp -O`:

```bash
./ssdov -root /srv/downloads -upload /srv/uploads
scp -O -P 2222 file.txt download@server-ip:file.txt
```

### Port already in use

Choose another port:

```bash
./ssdov -addr :2223 -root /srv/downloads
ssh -p 2223 download@server-ip
```

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build -o ssdov .
```

Format:

```bash
gofmt -w .
```

## Tech stack

- Go
- Charmbracelet Wish
- Bubble Tea
- Lip Gloss

## License

Add a license file before publishing if you want others to reuse the code formally.
