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
- Optional upload command and TUI upload helper
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

Run locally: ssh -p 2222 download@server 'upload photo.jpg' < '/home/me/photo.jpg'
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

Enable uploads by adding `-upload`:

```bash
export SSHDOWN_PASSWORD='change-this-password'
./ssdov -addr :2222 -root /srv/downloads -user download -upload
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

### Upload a file

Start the server with `-upload`, then upload from the client with stdin redirection:

```bash
ssh -p 2222 download@server-ip 'upload uploads/photo.jpg' < /local/path/photo.jpg
```

You can also press `u` in the TUI. SSDOV will ask you to paste or drag a local file path into the terminal, then lets you press Enter to keep the same filename or type a different remote filename. Because SSH TUIs cannot read client files directly, SSDOV shows the exact command to run locally.

## Direct commands

```text
ls [path]            list files under the download root
stat <path>          show file or directory info
download <file>      stream file bytes to stdout
upload <file>        stream stdin bytes into a new file (--upload required)
cat <file>           same as download
help                 show help
exit                 leave the basic shell
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

- SSDOV only serves files under `-root`.
- Absolute paths and `..` traversal are rejected.
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
