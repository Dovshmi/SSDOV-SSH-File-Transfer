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
	must(os.Mkdir(filepath.Join(root, "docs", "nested"), 0o755))
	must(os.WriteFile(filepath.Join(root, "docs", "nested", "note.txt"), []byte("note\n"), 0o644))
	return App{Root: root}
}

func TestChooseRootPathDefaultsToUploadDirWhenRootUnset(t *testing.T) {
	t.Setenv("SSHDOWN_ROOT", "")
	if got := chooseRootPath(".", "/srv/uploads", false); got != "/srv/uploads" {
		t.Fatalf("chooseRootPath root unset = %q, want upload dir", got)
	}
	if got := chooseRootPath("/srv/downloads", "/srv/uploads", true); got != "/srv/downloads" {
		t.Fatalf("chooseRootPath explicit root = %q, want explicit root", got)
	}
	t.Setenv("SSHDOWN_ROOT", "/env/root")
	if got := chooseRootPath("/env/root", "/srv/uploads", false); got != "/env/root" {
		t.Fatalf("chooseRootPath env root = %q, want env root", got)
	}
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

func TestSCPUploadDisabledRejectsUpload(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommandIO([]string{"scp", "-t", "new.txt"}, strings.NewReader(""), &out)
	if code == 0 {
		t.Fatalf("expected scp upload to fail when disabled")
	}
	if !strings.Contains(out.String(), "scp upload disabled") {
		t.Fatalf("expected upload disabled message, got %q", out.String())
	}
}

func TestSCPDownloadSourceStreamsFileProtocol(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommandIO([]string{"scp", "-f", "hello.txt"}, strings.NewReader("\x00\x00\x00"), &out)
	if code != 0 {
		t.Fatalf("scp download exit code = %d, output=%q", code, out.String())
	}
	want := "C0644 15 hello.txt\nhello over ssh\n\x00"
	if out.String() != want {
		t.Fatalf("scp download protocol output=%q, want %q", out.String(), want)
	}
}

func TestSCPDownloadRejectsDirectoriesAndTraversal(t *testing.T) {
	app := makeTestApp(t)
	for _, target := range []string{"docs", "../secret.txt", "/etc/passwd"} {
		var out bytes.Buffer
		code := app.RunCommandIO([]string{"scp", "-f", target}, strings.NewReader("\x00"), &out)
		if code == 0 {
			t.Fatalf("expected scp download %q to fail", target)
		}
	}
}

func TestSCPDownloadRecursiveFolderStreamsDirectoryProtocol(t *testing.T) {
	app := makeTestApp(t)
	var out bytes.Buffer
	code := app.RunCommandIO([]string{"scp", "-f", "-r", "docs"}, strings.NewReader(strings.Repeat("\x00", 10)), &out)
	if code != 0 {
		t.Fatalf("scp recursive download exit code = %d, output=%q", code, out.String())
	}
	want := "D0755 0 docs\n" +
		"D0755 0 nested\n" +
		"C0644 5 note.txt\nnote\n\x00" +
		"E\n" +
		"C0644 7 readme.md\n# docs\n\x00" +
		"E\n"
	if out.String() != want {
		t.Fatalf("scp recursive download protocol output=%q, want %q", out.String(), want)
	}
}

func TestSCPUploadEnabledWritesFileToUploadDir(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	var out bytes.Buffer
	input := "C0644 15 ignored.txt\nuploaded bytes\n\x00"
	code := app.RunCommandIO([]string{"scp", "-t", "server-name.txt"}, strings.NewReader(input), &out)
	if code != 0 {
		t.Fatalf("scp upload exit code = %d, output=%q", code, out.String())
	}
	got, err := os.ReadFile(filepath.Join(app.UploadDir, "server-name.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "uploaded bytes\n" {
		t.Fatalf("uploaded contents=%q", string(got))
	}
	if out.String() != "\x00\x00\x00" {
		t.Fatalf("scp ack output=%q, want three NUL acks", out.String())
	}
}

func TestSCPUploadRecursiveFlagStillAllowsSingleFile(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	var out bytes.Buffer
	input := "C0644 5 ignored.txt\nhello\x00"
	code := app.RunCommandIO([]string{"scp", "-t", "-r", "file.txt"}, strings.NewReader(input), &out)
	if code != 0 {
		t.Fatalf("scp upload with -r exit code = %d, output=%q", code, out.String())
	}
	got, err := os.ReadFile(filepath.Join(app.UploadDir, "file.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("uploaded contents=%q", string(got))
	}
}

func TestSCPUploadUsesProtocolFilenameWhenTargetIsDot(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	var out bytes.Buffer
	input := "C0644 5 photo.jpg\nhello\x00"
	code := app.RunCommandIO([]string{"scp", "-t", "."}, strings.NewReader(input), &out)
	if code != 0 {
		t.Fatalf("scp upload exit code = %d, output=%q", code, out.String())
	}
	got, err := os.ReadFile(filepath.Join(app.UploadDir, "photo.jpg"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("uploaded contents=%q", string(got))
	}
}

func TestSCPUploadRejectsPathTargetsAndOverwrite(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(app.UploadDir, "exists.txt"), []byte("old"), 0o644))

	for _, dest := range []string{"../evil.txt", "/tmp/evil.txt", "nested/file.txt", "exists.txt"} {
		var out bytes.Buffer
		code := app.RunCommandIO([]string{"scp", "-t", dest}, strings.NewReader("C0644 3 file.txt\nbad\x00"), &out)
		if code == 0 {
			t.Fatalf("expected scp upload %q to fail", dest)
		}
	}
}

func TestSCPUploadRecursiveFolderWritesDirectoryTree(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	var out bytes.Buffer
	input := "D0755 0 folder\n" +
		"C0644 5 a.txt\nhello\x00" +
		"D0755 0 nested\n" +
		"C0644 4 b.txt\nbye!\x00" +
		"E\n" +
		"E\n"
	code := app.RunCommandIO([]string{"scp", "-t", "-r", "."}, strings.NewReader(input), &out)
	if code != 0 {
		t.Fatalf("scp recursive upload exit code = %d, output=%q", code, out.String())
	}
	for path, want := range map[string]string{
		"folder/a.txt":        "hello",
		"folder/nested/b.txt": "bye!",
	} {
		got, err := os.ReadFile(filepath.Join(app.UploadDir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("uploaded recursive file %s missing: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("uploaded recursive file %s=%q, want %q", path, string(got), want)
		}
	}
	if out.String() != strings.Repeat("\x00", 9) {
		t.Fatalf("scp recursive ack output=%q, want nine NUL acks", out.String())
	}
}

func TestSCPUploadRecursiveRejectsDirectoryOverwrite(t *testing.T) {
	app := makeTestApp(t)
	app.UploadDir = t.TempDir()
	if err := os.Mkdir(filepath.Join(app.UploadDir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := app.RunCommandIO([]string{"scp", "-t", "-r", "."}, strings.NewReader("D0755 0 folder\nE\n"), &out)
	if code == 0 {
		t.Fatalf("expected recursive upload overwrite to fail")
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
