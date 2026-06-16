package main

import (
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
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/logging"
)

type App struct {
	Root string
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

func (a App) RunCommand(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, helpText())
		return 0
	}

	switch args[0] {
	case "help", "?":
		fmt.Fprintln(out, helpText())
		return 0
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

examples from your client machine:
  ssh -p 2222 download@server ls
  ssh -p 2222 download@server 'download docs/readme.md' > readme.md`)
}

func appMiddleware(app App) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			args := s.Command()
			if len(args) > 0 {
				code := app.RunCommand(args, s)
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
	var (
		addr    = flag.String("addr", envDefault("SSHDOWN_ADDR", ":2222"), "listen address; use :2222 to avoid normal sshd on port 22")
		root    = flag.String("root", envDefault("SSHDOWN_ROOT", "."), "download root directory")
		hostKey = flag.String("host-key", envDefault("SSHDOWN_HOST_KEY", "./sshdown_host_ed25519"), "SSH host key path; created by wish if missing")
		user    = flag.String("user", envDefault("SSHDOWN_USER", "download"), "allowed SSH username")
	)
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		log.Fatalf("download root must be an existing directory: %s", absRoot)
	}

	password := os.Getenv("SSHDOWN_PASSWORD")
	authKeys := os.Getenv("SSHDOWN_AUTHORIZED_KEYS")
	insecureAllowAny := os.Getenv("SSHDOWN_INSECURE_ALLOW_ANY") == "1"
	if password == "" && authKeys == "" && !insecureAllowAny {
		log.Fatal("refusing to start without auth; set SSHDOWN_PASSWORD, SSHDOWN_AUTHORIZED_KEYS, or SSHDOWN_INSECURE_ALLOW_ANY=1 for local testing")
	}

	app := App{Root: absRoot}
	opts := []ssh.Option{
		wish.WithAddress(*addr),
		wish.WithHostKeyPath(*hostKey),
		wish.WithMiddleware(appMiddleware(app), logging.Middleware()),
	}
	if password != "" || insecureAllowAny {
		opts = append(opts, wish.WithPasswordAuth(func(ctx ssh.Context, pass string) bool {
			if ctx.User() != *user {
				return false
			}
			return insecureAllowAny || pass == password
		}))
	}
	if authKeys != "" {
		opts = append(opts, wish.WithAuthorizedKeys(authKeys))
	}

	s, err := wish.NewServer(opts...)
	if err != nil {
		log.Fatal(err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()

	log.Printf("SSDOV listening on %s, root=%s, user=%s", *addr, absRoot, *user)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
