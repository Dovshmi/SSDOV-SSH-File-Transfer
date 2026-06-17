package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStartupServiceIncludesSelectedOptions(t *testing.T) {
	cfg := serverConfig{
		Addr:           ":2223",
		Root:           "/srv/downloads",
		UploadDir:      "/srv/uploads",
		HostKey:        "/home/me/.config/ssdov/host key",
		User:           "download",
		Password:       "secret",
		InstallService: true,
	}
	unit := buildStartupService(cfg, "/usr/local/bin/ssdov")
	for _, want := range []string{
		"[Unit]",
		"Description=SSDOV SSH file server",
		"Environment=SSHDOWN_PASSWORD=\"secret\"",
		"ExecStart=\"/usr/local/bin/ssdov\" -addr \":2223\" -root \"/srv/downloads\" -user \"download\" -host-key \"/home/me/.config/ssdov/host key\" -upload \"/srv/uploads\"",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("service unit missing %q:\n%s", want, unit)
		}
	}
}

func TestBuildStartupServiceOmitsUploadWhenDisabled(t *testing.T) {
	cfg := serverConfig{Addr: ":2223", Root: "/srv/downloads", HostKey: "/tmp/key", User: "download", Password: "secret"}
	unit := buildStartupService(cfg, "/usr/local/bin/ssdov")
	if strings.Contains(unit, " -upload ") {
		t.Fatalf("service unit should omit -upload when disabled:\n%s", unit)
	}
}

func TestWriteStartupServiceUsesUserConfigDirAnd0600(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	cfg := serverConfig{Addr: ":2223", Root: t.TempDir(), HostKey: "./hostkey", User: "download", Password: "secret"}
	path, err := writeStartupService(cfg, "/tmp/ssdov")
	if err != nil {
		t.Fatalf("writeStartupService returned error: %v", err)
	}
	wantPath := filepath.Join(configHome, "systemd", "user", "ssdov.service")
	if path != wantPath {
		t.Fatalf("service path=%q, want %q", path, wantPath)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("service file missing: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("service file mode=%#o, want 0600", got)
	}
}
