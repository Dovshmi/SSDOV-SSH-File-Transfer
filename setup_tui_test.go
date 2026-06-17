package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestShouldRunSetupWhenNoParameters(t *testing.T) {
	if !shouldRunSetup([]string{"ssdov"}) {
		t.Fatal("expected no-parameter startup to run setup menu")
	}
	if shouldRunSetup([]string{"ssdov", "-addr", ":2222"}) {
		t.Fatal("expected parameterized startup to keep existing flag/env startup path")
	}
}

func TestSetupTUIViewLooksModernAndMasksPassword(t *testing.T) {
	m := newSetupModel(defaultSetupConfig("."))
	m.localIP = "192.168.1.183"
	m.fields[setupFieldPassword] = "secret123"
	view := m.View()

	for _, want := range []string{
		"SSDOV Server Setup",
		"Server Main Menu",
		"Local IP: 192.168.1.183",
		"Port",
		"Download folder",
		"Upload folder",
		"SSH username",
		"Server password",
		"Start on boot",
		"disabled",
		"[ Enter Next ]",
		"[ Ctrl+C Cancel ]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("setup view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "secret123") {
		t.Fatalf("setup view leaked password:\n%s", view)
	}
	if !strings.Contains(view, "*********") {
		t.Fatalf("setup view did not mask password:\n%s", view)
	}
}

func TestSetupTUICollectsConfigAndFinishes(t *testing.T) {
	root := t.TempDir()
	upload := t.TempDir()
	m := newSetupModel(defaultSetupConfig(root))

	m.fields[setupFieldPort] = "2223"
	m.fields[setupFieldRoot] = root
	m.fields[setupFieldUpload] = upload
	m.fields[setupFieldUser] = "download"
	m.fields[setupFieldPassword] = "secret123"
	m.cursor = setupFieldConfirm

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if !m.done {
		t.Fatal("expected setup to finish on confirm")
	}
	cfg := m.config()
	if cfg.Addr != ":2223" || cfg.Root != root || cfg.UploadDir != upload || cfg.User != "download" || cfg.Password != "secret123" || cfg.InstallService {
		t.Fatalf("unexpected setup config: %#v", cfg)
	}
}

func TestSetupTUIStartupServiceCanBeEnabled(t *testing.T) {
	m := newSetupModel(defaultSetupConfig(t.TempDir()))
	m.cursor = setupFieldService
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if got := m.fields[setupFieldService]; got != "enabled" {
		t.Fatalf("service field after enter=%q, want enabled", got)
	}
	if cfg := m.config(); !cfg.InstallService {
		t.Fatalf("config InstallService=false after enabling service")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if got := m.fields[setupFieldService]; got != "disabled" {
		t.Fatalf("service field after second enter=%q, want disabled", got)
	}
}

func TestSetupTUIUploadDefaultsEmptyAndDisplaysOff(t *testing.T) {
	root := t.TempDir()
	m := newSetupModel(defaultSetupConfig(root))
	m.fields[setupFieldRoot] = root
	m.fields[setupFieldPassword] = "secret123"
	m.cursor = setupFieldConfirm

	if m.fields[setupFieldUpload] != "" {
		t.Fatalf("upload field default=%q, want empty so off is only display text", m.fields[setupFieldUpload])
	}
	view := m.View()
	if !strings.Contains(view, "off") || !strings.Contains(view, "SCP upload disabled") {
		t.Fatalf("setup view should display empty upload as off/disabled:\n%s", view)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if !m.done {
		t.Fatalf("expected setup to finish when upload is empty/off, message=%q", m.message)
	}
	cfg := m.config()
	if cfg.UploadDir != "" {
		t.Fatalf("empty upload produced UploadDir=%q, want disabled empty value", cfg.UploadDir)
	}
}

func TestSetupTUITypedOffDisablesUpload(t *testing.T) {
	root := t.TempDir()
	m := newSetupModel(defaultSetupConfig(root))
	m.fields[setupFieldRoot] = root
	m.fields[setupFieldUpload] = "off"
	m.fields[setupFieldPassword] = "secret123"
	m.cursor = setupFieldConfirm

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if !m.done {
		t.Fatalf("expected setup to finish when upload is typed off, message=%q", m.message)
	}
	if cfg := m.config(); cfg.UploadDir != "" {
		t.Fatalf("typed off produced UploadDir=%q, want disabled empty value", cfg.UploadDir)
	}
}

func TestSetupTUIUploadFolderShowsSCPOExplanationOnRight(t *testing.T) {
	root := t.TempDir()
	upload := t.TempDir()
	m := newSetupModel(defaultSetupConfig(root))
	m.fields[setupFieldRoot] = root
	m.fields[setupFieldUpload] = upload

	view := m.View()
	if !strings.Contains(view, upload+" ") || !strings.Contains(view, "enables scp -O uploads") {
		t.Fatalf("setup view should show folder with scp -O explanation on the right:\n%s", view)
	}
}

func TestValidateServerConfigUploadOffAliasesDisableUpload(t *testing.T) {
	root := t.TempDir()
	for _, value := range []string{"off", "OFF", " none ", "disabled", "no"} {
		cfg := serverConfig{Addr: ":2222", Root: root, UploadDir: value, User: "download", HostKey: "./hostkey", Password: "secret"}
		got, err := validateServerConfig(cfg)
		if err != nil {
			t.Fatalf("validateServerConfig(%q) returned error: %v", value, err)
		}
		if got.UploadDir != "" {
			t.Fatalf("validateServerConfig(%q) UploadDir=%q, want disabled empty value", value, got.UploadDir)
		}
	}
}

func TestSetupTUIValidatesRequiredPasswordAndFolders(t *testing.T) {
	m := newSetupModel(defaultSetupConfig("/definitely/missing/ssdov-root"))
	m.fields[setupFieldPassword] = ""
	m.cursor = setupFieldConfirm

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if m.done {
		t.Fatal("setup finished despite invalid inputs")
	}
	if !strings.Contains(m.message, "Password is required") {
		t.Fatalf("expected password validation first, got %q", m.message)
	}

	m.fields[setupFieldPassword] = "secret123"
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if !strings.Contains(m.message, "Folder does not exist") || !strings.Contains(m.message, "Press Y to create") {
		t.Fatalf("expected create-folder prompt, got %q", m.message)
	}
}

func TestSetupTUICreatesMissingDownloadFolderAfterPrompt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-root")
	m := newSetupModel(defaultSetupConfig(root))
	m.fields[setupFieldPassword] = "secret123"
	m.cursor = setupFieldConfirm

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if m.done {
		t.Fatal("setup finished before confirming folder creation")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("missing root exists before confirmation, err=%v", err)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(setupModel)
	if !m.done {
		t.Fatalf("setup did not finish after creating folder, message=%q", m.message)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("expected root folder to be created, info=%v err=%v", info, err)
	}
}

func TestSetupTUICreatesMissingUploadFolderAfterPrompt(t *testing.T) {
	root := t.TempDir()
	upload := filepath.Join(t.TempDir(), "new-upload")
	m := newSetupModel(defaultSetupConfig(root))
	m.fields[setupFieldUpload] = upload
	m.fields[setupFieldPassword] = "secret123"
	m.cursor = setupFieldConfirm

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(setupModel)
	if m.done {
		t.Fatal("setup finished before confirming upload folder creation")
	}
	if !strings.Contains(m.message, "Upload folder") || !strings.Contains(m.message, "Press Y to create") {
		t.Fatalf("expected upload create-folder prompt, got %q", m.message)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(setupModel)
	if !m.done {
		t.Fatalf("setup did not finish after creating upload folder, message=%q", m.message)
	}
	if info, err := os.Stat(upload); err != nil || !info.IsDir() {
		t.Fatalf("expected upload folder to be created, info=%v err=%v", info, err)
	}
}

func TestValidateServerConfigNormalizesPathsAndRequiresAuth(t *testing.T) {
	root := t.TempDir()
	upload := t.TempDir()
	cfg := serverConfig{Addr: ":2222", Root: root, UploadDir: upload, User: "download", HostKey: "./hostkey", Password: "secret"}
	got, err := validateServerConfig(cfg)
	if err != nil {
		t.Fatalf("validateServerConfig returned error: %v", err)
	}
	if got.Root != root || got.UploadDir != upload {
		t.Fatalf("validateServerConfig paths = root %q upload %q, want %q %q", got.Root, got.UploadDir, root, upload)
	}

	cfg.Password = ""
	cfg.AuthKeys = ""
	cfg.InsecureAllowAny = false
	if _, err := validateServerConfig(cfg); err == nil || !strings.Contains(err.Error(), "refusing to start without auth") {
		t.Fatalf("expected auth validation error, got %v", err)
	}
}
