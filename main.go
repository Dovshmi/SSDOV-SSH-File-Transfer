package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
)

type App struct {
	Root      string
	UploadDir string
}

type serverConfig struct {
	Addr             string
	Root             string
	UploadDir        string
	HostKey          string
	User             string
	Password         string
	AuthKeys         string
	InsecureAllowAny bool
	InstallService   bool
}

func (a App) UploadEnabled() bool {
	return a.UploadDir != ""
}

func (a App) ResolvePath(userPath string) (string, error) {
	root, err := filepath.Abs(a.Root)
	if err != nil {
		return "", err
	}
	if userPath == "" {
		userPath = "."
	}
	if filepath.IsAbs(userPath) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", userPath)
	}
	clean := filepath.Clean(userPath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes download root: %s", userPath)
	}
	full := filepath.Join(root, clean)
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes download root: %s", userPath)
	}
	return full, nil
}

func (a App) ResolveUploadPath(filename string) (string, error) {
	if !a.UploadEnabled() {
		return "", errors.New("scp upload disabled; restart server with -upload <directory>")
	}
	uploadDir, err := filepath.Abs(expandLeadingTilde(a.UploadDir))
	if err != nil {
		return "", err
	}
	if !isPlainFilename(filename) {
		return "", fmt.Errorf("upload destination must be a filename, not a path: %s", filename)
	}
	return filepath.Join(uploadDir, filename), nil
}

func isPlainFilename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) || strings.Contains(name, "/") || strings.Contains(name, `\\`) {
		return false
	}
	return filepath.Clean(name) == name
}

func (a App) RunCommand(args []string, out io.Writer) int {
	return a.RunCommandIO(args, strings.NewReader(""), out)
}

func (a App) RunCommandIO(args []string, in io.Reader, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, helpText())
		return 0
	}

	switch args[0] {
	case "help", "?":
		fmt.Fprintln(out, helpText())
		return 0
	case "scp":
		return a.runSCP(args[1:], in, out)
	case "ls":
		p := "."
		if len(args) > 1 {
			p = args[1]
		}
		return a.list(p, out)
	case "cat", "download", "get":
		if len(args) != 2 {
			fmt.Fprintln(out, "usage: download <file>")
			return 2
		}
		return a.download(args[1], out)
	case "stat":
		if len(args) != 2 {
			fmt.Fprintln(out, "usage: stat <path>")
			return 2
		}
		return a.stat(args[1], out)
	default:
		fmt.Fprintf(out, "unknown command: %s\n\n%s\n", args[0], helpText())
		return 2
	}
}

func (a App) list(userPath string, out io.Writer) int {
	p, err := a.ResolvePath(userPath)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintln(out, name)
	}
	return 0
}

func (a App) download(userPath string, out io.Writer) int {
	p, err := a.ResolvePath(userPath)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	info, err := os.Stat(p)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if !info.Mode().IsRegular() {
		fmt.Fprintf(out, "%s is not a regular file\n", userPath)
		return 1
	}
	f, err := os.Open(p)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer f.Close()
	if _, err := io.Copy(out, f); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	return 0
}

type scpMode int

const (
	scpModeNone scpMode = iota
	scpModeSink
	scpModeSource
)

type scpOptions struct {
	target    string
	mode      scpMode
	recursive bool
}

func (a App) runSCP(args []string, in io.Reader, out io.Writer) int {
	opts, err := parseSCPArgs(args)
	if err != nil {
		scpFatal(out, err.Error())
		return 1
	}
	switch opts.mode {
	case scpModeSink:
		return a.runSCPSink(opts.target, opts.recursive, in, out)
	case scpModeSource:
		return a.runSCPSource(opts.target, opts.recursive, in, out)
	default:
		scpFatal(out, "only legacy scp upload/download mode is supported; use scp -O -P <port> user@host:<file> .")
		return 1
	}
}

func (a App) runSCPSink(target string, recursive bool, in io.Reader, out io.Writer) int {
	if !a.UploadEnabled() {
		scpFatal(out, "scp upload disabled; restart server with -upload <directory>")
		return 1
	}
	uploadRoot, err := a.uploadRoot()
	if err != nil {
		scpFatal(out, err.Error())
		return 1
	}

	br := bufio.NewReader(in)
	if err := scpAck(out); err != nil {
		return 1
	}
	var dirStack []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			scpFatal(out, err.Error())
			return 1
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		switch line[0] {
		case 'T':
			if err := scpAck(out); err != nil {
				return 1
			}
		case 'D':
			if !recursive {
				scpFatal(out, "recursive scp directory upload requires scp -O -r")
				return 1
			}
			dir, err := a.receiveSCPDir(target, line, uploadRoot, dirStack)
			if err != nil {
				scpFatal(out, err.Error())
				return 1
			}
			dirStack = append(dirStack, dir)
			if err := scpAck(out); err != nil {
				return 1
			}
		case 'E':
			if len(dirStack) > 0 {
				dirStack = dirStack[:len(dirStack)-1]
			}
			if err := scpAck(out); err != nil {
				return 1
			}
			if len(dirStack) == 0 && recursive {
				return 0
			}
		case 'C':
			if len(dirStack) > 0 {
				if err := a.receiveSCPFileInDir(dirStack[len(dirStack)-1], line, br, out); err != nil {
					scpFatal(out, err.Error())
					return 1
				}
				continue
			}
			if err := a.receiveSCPFile(target, line, br, out); err != nil {
				scpFatal(out, err.Error())
				return 1
			}
		default:
			scpFatal(out, "unsupported scp protocol message")
			return 1
		}
	}
}

func (a App) runSCPSource(target string, recursive bool, in io.Reader, out io.Writer) int {
	if strings.TrimSpace(target) == "" {
		scpFatal(out, "usage: scp -O -P <port> user@host:<file> .")
		return 1
	}
	p, err := a.ResolvePath(target)
	if err != nil {
		scpFatal(out, err.Error())
		return 1
	}
	info, err := os.Stat(p)
	if err != nil {
		scpFatal(out, err.Error())
		return 1
	}
	if info.IsDir() {
		if !recursive {
			scpFatal(out, fmt.Sprintf("%s is a directory; use scp -O -r", target))
			return 1
		}
		if err := validateSCPSourceTree(p); err != nil {
			scpFatal(out, err.Error())
			return 1
		}
		if err := readSCPAck(in); err != nil {
			scpFatal(out, err.Error())
			return 1
		}
		if err := sendSCPDir(in, out, p, filepath.Base(filepath.Clean(target))); err != nil {
			scpFatal(out, err.Error())
			return 1
		}
		return 0
	}
	if !info.Mode().IsRegular() {
		scpFatal(out, fmt.Sprintf("%s is not a regular file", target))
		return 1
	}
	if err := readSCPAck(in); err != nil {
		scpFatal(out, err.Error())
		return 1
	}
	if err := sendSCPFile(in, out, p, filepath.Base(filepath.Clean(target))); err != nil {
		scpFatal(out, err.Error())
		return 1
	}
	return 0
}

func parseSCPArgs(args []string) (scpOptions, error) {
	var opts scpOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-t":
			if opts.mode != scpModeNone && opts.mode != scpModeSink {
				return scpOptions{}, errors.New("scp source and sink modes cannot be combined")
			}
			opts.mode = scpModeSink
		case "-f":
			if opts.mode != scpModeNone && opts.mode != scpModeSource {
				return scpOptions{}, errors.New("scp source and sink modes cannot be combined")
			}
			opts.mode = scpModeSource
		case "-r":
			opts.recursive = true
		case "-d", "-p", "-v":
			// -d is accepted for compatibility. -p may send timestamp
			// messages in sink mode, which we acknowledge and ignore.
		case "--":
			if i+1 < len(args) {
				opts.target = args[i+1]
			}
			return opts, nil
		default:
			if strings.HasPrefix(arg, "-") {
				return scpOptions{}, fmt.Errorf("unsupported scp option: %s", arg)
			}
			opts.target = arg
		}
	}
	return opts, nil
}

func readSCPAck(in io.Reader) error {
	var b [1]byte
	if _, err := io.ReadFull(in, b[:]); err != nil {
		return err
	}
	if b[0] == 0 {
		return nil
	}
	return fmt.Errorf("scp client returned status byte %d", b[0])
}

func validateSCPSourceTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported in recursive scp: %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() || info.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("unsupported file type in recursive scp: %s", path)
	})
}

func sendSCPDir(in io.Reader, out io.Writer, path, name string) error {
	if !isPlainFilename(name) {
		return fmt.Errorf("invalid scp directory name: %s", name)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm() & 0o777
	if mode == 0 {
		mode = 0o755
	}
	if _, err := fmt.Fprintf(out, "D%04o 0 %s\n", mode, name); err != nil {
		return err
	}
	if err := readSCPAck(in); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := sendSCPDir(in, out, entryPath, entry.Name()); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in recursive scp: %s", entryPath)
		}
		if err := sendSCPFile(in, out, entryPath, entry.Name()); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(out, "E\n"); err != nil {
		return err
	}
	return readSCPAck(in)
}

func sendSCPFile(in io.Reader, out io.Writer, path, name string) error {
	if !isPlainFilename(name) {
		return fmt.Errorf("invalid scp file name: %s", name)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	mode := info.Mode().Perm() & 0o777
	if mode == 0 {
		mode = 0o644
	}
	if _, err := fmt.Fprintf(out, "C%04o %d %s\n", mode, info.Size(), name); err != nil {
		return err
	}
	if err := readSCPAck(in); err != nil {
		return err
	}
	if _, err := io.Copy(out, f); err != nil {
		return err
	}
	if err := scpAck(out); err != nil {
		return err
	}
	return readSCPAck(in)
}

func (a App) uploadRoot() (string, error) {
	if !a.UploadEnabled() {
		return "", errors.New("scp upload disabled; restart server with -upload <directory>")
	}
	uploadRoot, err := filepath.Abs(expandLeadingTilde(a.UploadDir))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(uploadRoot)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("upload root is not a directory: %s", uploadRoot)
	}
	return uploadRoot, nil
}

func (a App) receiveSCPDir(target, header, uploadRoot string, dirStack []string) (string, error) {
	mode, protoName, err := parseSCPDirHeader(header)
	if err != nil {
		return "", err
	}
	dirName := protoName
	if len(dirStack) == 0 && target != "" && target != "." {
		dirName = target
	}
	parent := uploadRoot
	if len(dirStack) > 0 {
		parent = dirStack[len(dirStack)-1]
	}
	dest, err := uploadChildPath(uploadRoot, parent, dirName)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("%s already exists; refusing to overwrite", dirName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	perm := os.FileMode(mode) & 0o777
	if perm == 0 {
		perm = 0o755
	}
	if err := os.Mkdir(dest, perm); err != nil {
		return "", err
	}
	return dest, nil
}

func uploadChildPath(uploadRoot, parent, name string) (string, error) {
	if !isPlainFilename(name) {
		return "", fmt.Errorf("upload destination must be a plain filename, not a path: %s", name)
	}
	parent, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	uploadRoot, err = filepath.Abs(uploadRoot)
	if err != nil {
		return "", err
	}
	if parent != uploadRoot && !strings.HasPrefix(parent, uploadRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes upload root: %s", parent)
	}
	dest := filepath.Join(parent, name)
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if dest != uploadRoot && !strings.HasPrefix(dest, uploadRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes upload root: %s", name)
	}
	return dest, nil
}

func (a App) receiveSCPFile(target, header string, in *bufio.Reader, out io.Writer) error {
	mode, size, protoName, err := parseSCPFileHeader(header)
	if err != nil {
		return err
	}
	filename := target
	if filename == "" || filename == "." {
		filename = protoName
	}
	dest, err := a.ResolveUploadPath(filename)
	if err != nil {
		return err
	}
	return receiveSCPFileAt(dest, filename, mode, size, in, out)
}

func (a App) receiveSCPFileInDir(parent, header string, in *bufio.Reader, out io.Writer) error {
	mode, size, protoName, err := parseSCPFileHeader(header)
	if err != nil {
		return err
	}
	uploadRoot, err := a.uploadRoot()
	if err != nil {
		return err
	}
	dest, err := uploadChildPath(uploadRoot, parent, protoName)
	if err != nil {
		return err
	}
	return receiveSCPFileAt(dest, protoName, mode, size, in, out)
}

func receiveSCPFileAt(dest, displayName string, mode uint64, size int64, in *bufio.Reader, out io.Writer) error {
	if info, err := os.Stat(dest); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory; choose a destination filename", displayName)
		}
		return fmt.Errorf("%s already exists; refusing to overwrite", displayName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	parent := filepath.Dir(dest)
	tmp, err := os.CreateTemp(parent, ".ssdov-scp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := scpAck(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.CopyN(tmp, in, size); err != nil {
		_ = tmp.Close()
		return err
	}
	end, err := in.ReadByte()
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if end != 0 {
		_ = tmp.Close()
		return errors.New("invalid scp file terminator")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	perm := os.FileMode(mode) & 0o666
	if perm == 0 {
		perm = 0o644
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	ok = true
	return scpAck(out)
}

func parseSCPFileHeader(header string) (mode uint64, size int64, filename string, err error) {
	parts := strings.SplitN(header, " ", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "C") {
		return 0, 0, "", fmt.Errorf("invalid scp file header")
	}
	mode, err = strconv.ParseUint(parts[0][1:], 8, 32)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid scp file mode")
	}
	size, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || size < 0 {
		return 0, 0, "", fmt.Errorf("invalid scp file size")
	}
	filename = parts[2]
	if !isPlainFilename(filename) {
		return 0, 0, "", fmt.Errorf("invalid scp file name: %s", filename)
	}
	return mode, size, filename, nil
}

func parseSCPDirHeader(header string) (mode uint64, dirname string, err error) {
	parts := strings.SplitN(header, " ", 3)
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "D") {
		return 0, "", fmt.Errorf("invalid scp directory header")
	}
	mode, err = strconv.ParseUint(parts[0][1:], 8, 32)
	if err != nil {
		return 0, "", fmt.Errorf("invalid scp directory mode")
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return 0, "", fmt.Errorf("invalid scp directory size")
	}
	dirname = parts[2]
	if !isPlainFilename(dirname) {
		return 0, "", fmt.Errorf("invalid scp directory name: %s", dirname)
	}
	return mode, dirname, nil
}

func scpAck(out io.Writer) error {
	_, err := out.Write([]byte{0})
	return err
}

func scpFatal(out io.Writer, msg string) {
	_, _ = fmt.Fprintf(out, "\x01%s\n", msg)
}

func (a App) stat(userPath string, out io.Writer) int {
	p, err := a.ResolvePath(userPath)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	info, err := os.Stat(p)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	}
	fmt.Fprintf(out, "%s\t%d bytes\t%s\n", kind, info.Size(), userPath)
	return 0
}

func (a App) RunInteractive(rw io.ReadWriter) {
	fmt.Fprintf(rw, "SSDOV\nroot: %s\n\n%s\n", a.Root, helpText())
	for {
		fmt.Fprint(rw, "ssdov> ")
		line, err := readInteractiveLineEcho(rw, rw)
		if err != nil && line == "" {
			fmt.Fprintln(rw)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil {
				return
			}
			continue
		}
		if line == "exit" || line == "quit" {
			fmt.Fprintln(rw, "bye")
			return
		}
		args := strings.Fields(line)
		a.RunCommand(args, rw)
		if err != nil {
			return
		}
	}
}

func readInteractiveLine(r io.Reader) (string, error) {
	return readInteractiveLineEcho(r, nil)
}

func readInteractiveLineEcho(r io.Reader, echo io.Writer) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			switch buf[0] {
			case '\r', '\n':
				if echo != nil {
					_, _ = io.WriteString(echo, "\r\n")
				}
				return b.String(), nil
			case 0x08, 0x7f: // Backspace/Delete from terminals.
				s := b.String()
				if len(s) > 0 {
					b.Reset()
					b.WriteString(s[:len(s)-1])
					if echo != nil {
						_, _ = io.WriteString(echo, "\b \b")
					}
				}
			default:
				b.WriteByte(buf[0])
				if echo != nil {
					_, _ = echo.Write(buf[:1])
				}
			}
		}
		if err != nil {
			if b.Len() > 0 {
				return b.String(), err
			}
			return "", err
		}
	}
}

func helpText() string {
	return strings.TrimSpace(`commands:
  ls [path]            list files under the download root
  stat <path>          show file/dir info
  download <file>      stream file bytes to stdout
  cat <file>           same as download
  help                 show this help
  exit                 leave interactive shell

scp download:
  scp -O -P 2222 download@server:docs/readme.md .
  scp -O -r -P 2222 download@server:docs .

scp upload, if server was started with -upload <directory>:
  scp -O -P 2222 local-file download@server:server-name
  scp -O -r -P 2222 local-folder download@server:folder-name

examples from your client machine:
  ssh -p 2222 download@server ls
  ssh -p 2222 download@server 'download docs/readme.md' > readme.md
  scp -O -P 2222 download@server:docs/readme.md .
  scp -O -r -P 2222 download@server:docs .
  scp -O -P 2222 photo.jpg download@server:photo.jpg
  scp -O -r -P 2222 myfolder download@server:myfolder`)
}

func appMiddleware(app App) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			args := s.Command()
			if len(args) > 0 {
				code := app.RunCommandIO(args, s, s)
				_ = s.Exit(code)
				return
			}
			if _, _, ok := s.Pty(); ok {
				if err := runTUIInSession(app, s); err != nil {
					fmt.Fprintf(s, "TUI failed: %v\r\nfalling back to basic shell\r\n", err)
					app.RunInteractive(s)
				}
				_ = s.Exit(0)
				return
			}
			app.RunInteractive(s)
			_ = s.Exit(0)
		}
	}
}

func main() {
	cfg, ok, err := startupConfig(os.Args)
	if err != nil {
		log.Fatal(err)
	}
	if !ok {
		return
	}
	if err := startServer(cfg); err != nil {
		log.Fatal(err)
	}
}

func startupConfig(args []string) (serverConfig, bool, error) {
	if shouldRunSetup(args) {
		return runSetupMenu(defaultSetupConfig("."))
	}
	return flagServerConfig(), true, nil
}

func shouldRunSetup(args []string) bool {
	return len(args) == 1
}

func flagServerConfig() serverConfig {
	var (
		addr      = flag.String("addr", envDefault("SSHDOWN_ADDR", ":2222"), "listen address; use :2222 to avoid normal sshd on port 22")
		root      = flag.String("root", envDefault("SSHDOWN_ROOT", "."), "download root directory")
		hostKey   = flag.String("host-key", envDefault("SSHDOWN_HOST_KEY", "./sshdown_host_ed25519"), "SSH host key path; created by wish if missing")
		user      = flag.String("user", envDefault("SSHDOWN_USER", "download"), "allowed SSH username")
		uploadDir = flag.String("upload", envDefault("SSHDOWN_UPLOAD_DIR", ""), "enable legacy scp -O uploads into this existing directory")
	)
	flag.Parse()

	rootExplicit := rootFlagExplicitlySet()
	rootPath := chooseRootPath(*root, *uploadDir, rootExplicit)
	return serverConfig{
		Addr:             *addr,
		Root:             rootPath,
		UploadDir:        *uploadDir,
		HostKey:          *hostKey,
		User:             *user,
		Password:         os.Getenv("SSHDOWN_PASSWORD"),
		AuthKeys:         os.Getenv("SSHDOWN_AUTHORIZED_KEYS"),
		InsecureAllowAny: os.Getenv("SSHDOWN_INSECURE_ALLOW_ANY") == "1",
	}
}

func validateServerConfig(cfg serverConfig) (serverConfig, error) {
	cfg.Addr = normalizeListenAddr(cfg.Addr)
	if err := validateListenAddr(cfg.Addr); err != nil {
		return cfg, err
	}
	cfg.User = strings.TrimSpace(cfg.User)
	if cfg.User == "" {
		return cfg, errors.New("SSH username is required")
	}
	if strings.TrimSpace(cfg.HostKey) == "" {
		cfg.HostKey = "./sshdown_host_ed25519"
	}
	if strings.TrimSpace(cfg.Root) == "" {
		cfg.Root = "."
	}

	absRoot, err := filepath.Abs(expandLeadingTilde(cfg.Root))
	if err != nil {
		return cfg, err
	}
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		return cfg, fmt.Errorf("Download folder must exist: %s", absRoot)
	}
	cfg.Root = absRoot

	cfg.UploadDir = normalizeUploadDir(cfg.UploadDir)
	if cfg.UploadDir != "" {
		absUploadDir, err := filepath.Abs(expandLeadingTilde(cfg.UploadDir))
		if err != nil {
			return cfg, err
		}
		if info, err := os.Stat(absUploadDir); err != nil || !info.IsDir() {
			return cfg, fmt.Errorf("Upload folder must exist: %s", absUploadDir)
		}
		cfg.UploadDir = absUploadDir
	}

	if cfg.Password == "" && cfg.AuthKeys == "" && !cfg.InsecureAllowAny {
		return cfg, errors.New("refusing to start without auth; set a server password, SSHDOWN_AUTHORIZED_KEYS, or SSHDOWN_INSECURE_ALLOW_ANY=1 for local testing")
	}
	return cfg, nil
}

func normalizeListenAddr(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ":2222"
	}
	if _, err := strconv.Atoi(value); err == nil {
		return ":" + value
	}
	return value
}

func normalizeUploadDir(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "off", "none", "disabled", "no":
		return ""
	default:
		return value
	}
}

func validateListenAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q; use a port like 2222 or an address like :2222", addr)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid port %q; choose a number from 1 to 65535", port)
	}
	return nil
}

func listenPort(addr string) string {
	_, port, err := net.SplitHostPort(normalizeListenAddr(addr))
	if err == nil && port != "" {
		return port
	}
	return portFromAddr(addr)
}

func startServer(cfg serverConfig) error {
	cfg, err := validateServerConfig(cfg)
	if err != nil {
		return err
	}

	app := App{Root: cfg.Root, UploadDir: cfg.UploadDir}
	if cfg.InstallService {
		servicePath, err := installStartupService(cfg)
		if err != nil {
			return err
		}
		log.Printf("Installed startup service: %s", servicePath)
	}
	opts := []ssh.Option{
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKey),
		wish.WithMiddleware(appMiddleware(app), logging.Middleware()),
	}
	if cfg.Password != "" || cfg.InsecureAllowAny {
		opts = append(opts, wish.WithPasswordAuth(func(ctx ssh.Context, pass string) bool {
			if ctx.User() != cfg.User {
				return false
			}
			return cfg.InsecureAllowAny || pass == cfg.Password
		}))
	}
	if cfg.AuthKeys != "" {
		opts = append(opts, wish.WithAuthorizedKeys(cfg.AuthKeys))
	}

	s, err := wish.NewServer(opts...)
	if err != nil {
		return err
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	localIP := detectLocalIP()
	log.Printf("SSDOV listening on %s, root=%s, upload=%s, user=%s", cfg.Addr, cfg.Root, cfg.UploadDir, cfg.User)
	log.Printf("Client TUI: ssh -p %s %s@%s", listenPort(cfg.Addr), cfg.User, localIP)
	if cfg.UploadDir != "" {
		log.Printf("Client upload: scp -O -P %s local-file %s@%s:filename", listenPort(cfg.Addr), cfg.User, localIP)
	}
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func chooseRootPath(rootPath, uploadDir string, rootExplicit bool) string {
	if !rootExplicit && os.Getenv("SSHDOWN_ROOT") == "" && strings.TrimSpace(uploadDir) != "" {
		return uploadDir
	}
	return rootPath
}

func rootFlagExplicitlySet() bool {
	explicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "root" {
			explicit = true
		}
	})
	return explicit
}

func expandLeadingTilde(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
