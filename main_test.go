package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scriptedRW struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func newScriptedRW(script string) *scriptedRW {
	return &scriptedRW{in: bytes.NewReader([]byte(script))}
}

func (rw *scriptedRW) Read(p []byte) (int, error) {
	return rw.in.Read(p)
}

func (rw *scriptedRW) Write(p []byte) (int, error) {
	return rw.out.Write(p)
}

func (rw *scriptedRW) String() string { return rw.out.String() }

func makeTestApp(t *testing.T) App {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello over ssh\n"), 0o644))
	must(os.Mkdir(filepath.Join(root, "docs"), 0o755))
	must(os.WriteFile(filepath.Join(root, "docs", "readme.md"), []byte("# docs\n"), 0o644))
	return App{Root: root}
}

func TestResolvePathKeepsDownloadsInsideRoot(t *testing.T) {
	app := makeTestApp(t)
	p, err := app.ResolvePath("docs/readme.md")
	if err != nil {
		t.Fatalf("ResolvePath returned error: %v", err)
	}
	if !strings.HasPrefix(p, app.Root+string(os.PathSeparator)) {
		t.Fatalf("resolved path escaped root: %q not under %q", p, app.Root)
	}

	if _, err := app.ResolvePath("../secret.txt"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
	if _, err := app.ResolvePath("/etc/passwd"); err == nil {
		t.Fatal("expected absolute path outside root to be rejected")
	}
}

func TestDownloadCommandStreamsFileBytes(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommand([]string{"download", "hello.txt"}, &out)
	if code != 0 {
		t.Fatalf("RunCommand exit code = %d, want 0; output=%q", code, out.String())
	}
	if got := out.String(); got != "hello over ssh\n" {
		t.Fatalf("download output = %q", got)
	}
}

func TestLsCommandListsRelativeFiles(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommand([]string{"ls", "."}, &out)
	if code != 0 {
		t.Fatalf("RunCommand exit code = %d, want 0; output=%q", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"docs/", "hello.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ls output %q does not contain %q", got, want)
		}
	}
}

func TestDownloadDirectoryIsRejected(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommand([]string{"download", "docs"}, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit code for directory download, got 0")
	}
	if !strings.Contains(out.String(), "not a regular file") {
		t.Fatalf("expected regular-file error, got %q", out.String())
	}
}

func TestInteractiveAcceptsCarriageReturnFromPTYClients(t *testing.T) {
	app := makeTestApp(t)
	rw := newScriptedRW("ls\rexit\r")
	app.RunInteractive(rw)
	got := rw.String()
	if strings.Contains(got, "open ") {
		t.Fatalf("interactive shell treated CR-separated commands as one command: %q", got)
	}
	if !strings.Contains(got, "hello.txt") {
		t.Fatalf("interactive shell did not run ls command, output=%q", got)
	}
	if !strings.Contains(got, "bye") {
		t.Fatalf("interactive shell did not run exit command, output=%q", got)
	}
}

func TestInteractiveEchoesTypedCommands(t *testing.T) {
	app := makeTestApp(t)
	rw := newScriptedRW("ls\rexit\r")
	app.RunInteractive(rw)
	got := rw.String()
	if !strings.Contains(got, "ssdov> ls\r\n") {
		t.Fatalf("interactive shell did not echo typed ls command with newline, output=%q", got)
	}
	if !strings.Contains(got, "ssdov> exit\r\n") {
		t.Fatalf("interactive shell did not echo typed exit command with newline, output=%q", got)
	}
}

func TestReadInteractiveLineHandlesCRLFAndEOF(t *testing.T) {
	line, err := readInteractiveLine(bytes.NewBufferString("stat hello.txt\r\nrest"))
	if err != nil {
		t.Fatalf("readInteractiveLine returned error: %v", err)
	}
	if line != "stat hello.txt" {
		t.Fatalf("line=%q, want stat hello.txt", line)
	}

	line, err = readInteractiveLine(bytes.NewBufferString("exit"))
	if err != nil && err != io.EOF {
		t.Fatalf("readInteractiveLine EOF returned unexpected error: %v", err)
	}
	if line != "exit" {
		t.Fatalf("line=%q, want exit", line)
	}
}
