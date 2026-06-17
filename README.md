# SSDOV — SSH Downloader

SSDOV is a small SSH application for browsing and downloading files from a NAS or Linux server.

It runs as its own SSH server on a separate port, so your normal SSH server on port 22 keeps working normally.

Default SSDOV port: `2222`

## Features

- SSH-based file browser and downloader
- Modern Bubble Tea + Lip Gloss terminal UI
- File/folder navigation over SSH
- File preview in the TUI
- Download command hints for selected files
- Optional legacy SCP upload support (`scp -O`) into a server-chosen upload folder
- Direct non-interactive download commands for scripts
- Password auth or public-key auth
- Path traversal protection: users stay inside the configured download root
- Does not replace or modify normal OpenSSH on port 22

## Screenshot-style preview

```text
SSDOV ⠋
root /

› 📁  docs/                              folder
  📄  hello.txt                            24 B

Run locally: scp -O -P 2222 '/home/me/photo.jpg' 'download@server:photo.jpg'
[ Enter Open/View ] [ D Download ] [ U Upload ] [ B Back ] [ R Refresh ] [ Q Quit ]
```

## Build

Requires Go.

```bash
go build -o ssdov .
```

## Run on the server

Choose a directory that SSDOV is allowed to serve, for example `/srv/downloads`.

### Password auth

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -root /srv/downloads -user download
```

Run with legacy SCP uploads enabled by choosing a server-side upload directory. If you omit `-root`, the TUI/download root defaults to the same directory:

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -user download -upload ~/Downloads
```

Use separate folders only if you want browsing/downloads and uploads to differ:

```bash
./ssdov -addr :2222 -root /srv/downloads -user download -upload ~/Downloads
```

### Public-key auth

```bash
export SSHDOWN_AUTHORIZED_KEYS=/home/youruser/.ssh/authorized_keys
./ssdov -addr :2222 -root /srv/downloads -user download
```

Normal SSH remains available on port 22. SSDOV listens on port 2222 unless you choose another port.

## Use from a client

### Open the interactive TUI

```bash
ssh -p 2222 download@server-ip
```

TUI keys:

```text
↑/↓ or j/k       move selection
Enter            open folder or preview file
d                show download command for selected file
u                upload helper: paste/drag local path, then keep or rename
b / Backspace    go back
r                refresh
q / Esc          quit
```

### List files without opening the TUI

```bash
ssh -p 2222 download@server-ip ls
ssh -p 2222 download@server-ip 'ls docs'
```

### Download a file

```bash
ssh -p 2222 download@server-ip 'download docs/readme.md' > readme.md
```

Direct download mode writes the file bytes to stdout, so redirects and scripts work naturally.

### Upload a file with SCP

Start the server with `-upload <server-directory>`, then upload from the client with legacy SCP mode:

```bash
scp -O -P 2222 /local/path/photo.jpg download@server-ip:photo.jpg
```

SSDOV saves the file as `photo.jpg` inside the server directory passed to `-upload`. The client chooses the server filename, but not the server folder.

You can also press `u` in the TUI. SSDOV will ask you to paste or drag a local file path into the terminal, then ask for the server filename, and then show the exact `scp -O` command to run locally.

## Direct commands

```text
ls [path]            list files under the download root
stat <path>          show file or directory info
download <file>      stream file bytes to stdout
cat <file>           same as download
help                 show help
exit                 leave the basic shell
```

Upload uses legacy SCP instead of a direct shell command:

```bash
scp -O -P 2222 /local/file.jpg download@server-ip:file.jpg
```

## Systemd user service example

Edit `ssh-downloader.service.example`, then copy it to:

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

## Security notes

- SSDOV only serves downloads from files under `-root`.
- Absolute paths and `..` traversal are rejected for download paths.
- SCP uploads are disabled unless `-upload <directory>` is set.
- SCP upload destinations must be plain filenames; the server always saves them inside the `-upload` directory.
- Set `SSHDOWN_PASSWORD` or `SSHDOWN_AUTHORIZED_KEYS` before starting.
- `SSHDOWN_INSECURE_ALLOW_ANY=1` is only for local testing.
- Do not commit generated host keys or passwords to GitHub.

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build -o ssdov .
```

## Tech stack

- Go
- Charmbracelet Wish
- Bubble Tea
- Lip Gloss

## License

Add a license file before publishing if you want others to reuse the code formally.
