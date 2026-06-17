package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type setupField int

const (
	setupFieldPort setupField = iota
	setupFieldRoot
	setupFieldUpload
	setupFieldUser
	setupFieldPassword
	setupFieldService
	setupFieldConfirm
	setupFieldCount = setupFieldConfirm
)

type setupModel struct {
	fields             []string
	cursor             setupField
	message            string
	localIP            string
	pendingCreatePath  string
	pendingCreateField setupField
	done               bool
	cancelled          bool
}

var (
	setupTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	setupCardStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).BorderForeground(lipgloss.Color("63"))
	setupFocusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("63")).Bold(true)
	setupDimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	setupErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
)

func defaultSetupConfig(defaultRoot string) serverConfig {
	return serverConfig{
		Addr:             envDefault("SSHDOWN_ADDR", ":2222"),
		Root:             envDefault("SSHDOWN_ROOT", defaultRoot),
		UploadDir:        envDefault("SSHDOWN_UPLOAD_DIR", ""),
		HostKey:          envDefault("SSHDOWN_HOST_KEY", "./sshdown_host_ed25519"),
		User:             envDefault("SSHDOWN_USER", "download"),
		Password:         "",
		AuthKeys:         "",
		InsecureAllowAny: false,
		InstallService:   false,
	}
}

func newSetupModel(cfg serverConfig) setupModel {
	serviceState := "disabled"
	if cfg.InstallService {
		serviceState = "enabled"
	}
	return setupModel{
		fields: []string{
			portFromAddr(cfg.Addr),
			cfg.Root,
			cfg.UploadDir,
			cfg.User,
			cfg.Password,
			serviceState,
		},
		message: "Fill the server options. Upload folder is optional.",
		localIP: detectLocalIP(),
	}
}

func runSetupMenu(cfg serverConfig) (serverConfig, bool, error) {
	model, err := tea.NewProgram(newSetupModel(cfg), tea.WithAltScreen()).Run()
	if err != nil {
		return serverConfig{}, false, err
	}
	m, ok := model.(setupModel)
	if !ok || m.cancelled || !m.done {
		return serverConfig{}, false, nil
	}
	return m.config(), true, nil
}

func (m setupModel) Init() tea.Cmd { return nil }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.pendingCreatePath != "" {
			return m.updateCreatePrompt(msg)
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "shift+tab":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "tab":
			if m.cursor < setupFieldConfirm {
				m.cursor++
			}
		case " ":
			if m.cursor == setupFieldService {
				m.toggleService()
			}
		case "enter":
			if m.cursor == setupFieldService {
				m.toggleService()
				return m, nil
			}
			if m.cursor == setupFieldConfirm {
				return m.finishSetup()
			}
			if m.cursor < setupFieldConfirm {
				m.cursor++
			}
		case "backspace":
			m.backspace()
		default:
			if msg.Type == tea.KeyRunes && m.isEditableField(m.cursor) {
				m.fields[m.cursor] += string(msg.Runes)
			}
		}
	}
	return m, nil
}

func (m setupModel) updateCreatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		if err := os.MkdirAll(m.pendingCreatePath, 0o755); err != nil {
			m.message = fmt.Sprintf("Could not create folder: %v", err)
			return m, nil
		}
		created := m.pendingCreatePath
		m.pendingCreatePath = ""
		m.message = "Created folder: " + created
		return m.finishSetup()
	case "n":
		m.cursor = m.pendingCreateField
		m.pendingCreatePath = ""
		m.message = "Folder not created. Edit the folder path, or press Enter again to create it."
		return m, nil
	case "ctrl+c", "esc":
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m setupModel) finishSetup() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.fields[setupFieldPassword]) == "" {
		m.message = "Password is required."
		return m, nil
	}
	if field, label, path, ok, err := missingSetupFolder(m.config()); err != nil {
		m.message = err.Error()
		return m, nil
	} else if ok {
		m.pendingCreateField = field
		m.pendingCreatePath = path
		m.message = fmt.Sprintf("Folder does not exist: %s (%s). Press Y to create it, N to edit.", path, label)
		return m, nil
	}
	if _, err := validateServerConfig(m.config()); err != nil {
		m.message = err.Error()
		return m, nil
	}
	m.done = true
	return m, tea.Quit
}

func missingSetupFolder(cfg serverConfig) (setupField, string, string, bool, error) {
	root, err := filepath.Abs(expandLeadingTilde(strings.TrimSpace(cfg.Root)))
	if err != nil {
		return 0, "", "", false, err
	}
	if info, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return setupFieldRoot, "Download folder", root, true, nil
		}
		return 0, "", "", false, err
	} else if !info.IsDir() {
		return 0, "", "", false, fmt.Errorf("Download folder must be a directory: %s", root)
	}

	upload := normalizeUploadDir(cfg.UploadDir)
	if upload == "" {
		return 0, "", "", false, nil
	}
	upload, err = filepath.Abs(expandLeadingTilde(upload))
	if err != nil {
		return 0, "", "", false, err
	}
	if info, err := os.Stat(upload); err != nil {
		if os.IsNotExist(err) {
			return setupFieldUpload, "Upload folder", upload, true, nil
		}
		return 0, "", "", false, err
	} else if !info.IsDir() {
		return 0, "", "", false, fmt.Errorf("Upload folder must be a directory: %s", upload)
	}
	return 0, "", "", false, nil
}

func (m *setupModel) backspace() {
	if !m.isEditableField(m.cursor) {
		return
	}
	value := m.fields[m.cursor]
	if value == "" {
		return
	}
	runes := []rune(value)
	m.fields[m.cursor] = string(runes[:len(runes)-1])
}

func (m setupModel) isEditableField(field setupField) bool {
	return field >= setupFieldPort && field <= setupFieldPassword
}

func (m *setupModel) toggleService() {
	if m.fields[setupFieldService] == "enabled" {
		m.fields[setupFieldService] = "disabled"
		return
	}
	m.fields[setupFieldService] = "enabled"
}

func (m setupModel) View() string {
	var b strings.Builder
	b.WriteString(setupTitleStyle.Render("SSDOV Server Setup"))
	b.WriteString("\n")
	b.WriteString(setupDimStyle.Render("Server Main Menu — configure and start the SSH file server"))
	b.WriteString("\n")
	b.WriteString(setupDimStyle.Render("Local IP: " + m.localIP))
	b.WriteString("\n\n")

	rows := []string{
		m.renderField(setupFieldPort, "Port", "2222"),
		m.renderField(setupFieldRoot, "Download folder", "folder clients can browse/download"),
		m.renderField(setupFieldUpload, "Upload folder", "off, or a folder path to enable uploads"),
		m.renderField(setupFieldUser, "SSH username", "download"),
		m.renderField(setupFieldPassword, "Server password", "required; not saved"),
		m.renderField(setupFieldService, "Start on boot", "disabled"),
		m.renderConfirm(),
	}
	b.WriteString(setupCardStyle.Render(strings.Join(rows, "\n")))
	b.WriteString("\n\n")
	if m.message != "" {
		style := setupDimStyle
		lower := strings.ToLower(m.message)
		if strings.Contains(lower, "must") || strings.Contains(lower, "required") || strings.Contains(lower, "refusing") || strings.Contains(lower, "invalid") || strings.Contains(lower, "could not") || strings.Contains(lower, "does not exist") {
			style = setupErrorStyle
		}
		b.WriteString(style.Render(m.message))
		b.WriteString("\n")
	}
	b.WriteString(setupDimStyle.Render("[ ↑/↓ Move ]  [ Enter Next ]  [ Space Toggle ]  [ Backspace Edit ]  [ Ctrl+C Cancel ]"))
	return b.String()
}

func (m setupModel) renderField(field setupField, label, hint string) string {
	value := m.fields[field]
	if field == setupFieldPassword && value != "" {
		value = strings.Repeat("*", len([]rune(value)))
	}
	if field == setupFieldUpload {
		value = renderUploadValue(value)
	}
	if field == setupFieldService {
		value = renderServiceValue(value)
	}
	if value == "" {
		value = setupDimStyle.Render(hint)
	}
	line := fmt.Sprintf("%-16s %s", label+":", value)
	if m.cursor == field {
		return setupFocusStyle.Render("› " + line)
	}
	return "  " + line
}

func renderUploadValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if normalizeUploadDir(trimmed) == "" {
		return setupDimStyle.Render("off (SCP upload disabled)")
	}
	return trimmed + " " + setupDimStyle.Render("(enables scp -O uploads)")
}

func renderServiceValue(value string) string {
	if value == "enabled" {
		return "enabled " + setupDimStyle.Render("(install user startup service)")
	}
	return setupDimStyle.Render("disabled")
}

func (m setupModel) renderConfirm() string {
	line := "Start server with these options"
	if m.cursor == setupFieldConfirm {
		return setupFocusStyle.Render("› " + line)
	}
	return "  " + line
}

func (m setupModel) config() serverConfig {
	return serverConfig{
		Addr:           normalizeListenAddr(m.fields[setupFieldPort]),
		Root:           strings.TrimSpace(m.fields[setupFieldRoot]),
		UploadDir:      normalizeUploadDir(m.fields[setupFieldUpload]),
		HostKey:        envDefault("SSHDOWN_HOST_KEY", "./sshdown_host_ed25519"),
		User:           strings.TrimSpace(m.fields[setupFieldUser]),
		Password:       m.fields[setupFieldPassword],
		InstallService: m.fields[setupFieldService] == "enabled",
	}
}

func portFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, ":") && !strings.Contains(strings.TrimPrefix(addr, ":"), ":") {
		return strings.TrimPrefix(addr, ":")
	}
	return addr
}
