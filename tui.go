package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	wishbt "github.com/charmbracelet/wish/bubbletea"
)

type tickMsg time.Time

type tuiMode int

const (
	tuiModeBrowse tuiMode = iota
	tuiModeUploadPath
	tuiModeUploadName
)

type tuiEntry struct {
	Name  string
	IsDir bool
	Size  int64
}

type tuiModel struct {
	app              App
	relDir           string
	entries          []tuiEntry
	cursor           int
	width            int
	height           int
	message          string
	copyCommand      string
	viewer           string
	tick             int
	remote           string
	port             int
	lastError        string
	mode             tuiMode
	input            string
	uploadLocalPath  string
	uploadRemoteName string
}

func runTUIInSession(app App, s ssh.Session) error {
	_, _, ok := s.Pty()
	if !ok {
		app.RunInteractive(s)
		return nil
	}

	local := s.LocalAddr().String()
	host, port := splitHostPortDefault(local, "server", 2222)
	remote := fmt.Sprintf("%s@%s", s.User(), host)
	m := newTUIModel(app, remote, port)
	opts := append(wishbt.MakeOptions(s), tea.WithAltScreen())
	_, err := tea.NewProgram(m, opts...).Run()
	return err
}

func splitHostPortDefault(addr, fallbackHost string, fallbackPort int) (string, int) {
	host := fallbackHost
	port := fallbackPort
	parts := strings.Split(addr, ":")
	if len(parts) >= 2 {
		if parts[0] != "" && parts[0] != "0.0.0.0" && parts[0] != "::" && parts[0] != "[::]" {
			host = strings.Trim(parts[0], "[]")
		}
		var p int
		if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &p); err == nil && p > 0 {
			port = p
		}
	}
	return host, port
}

func newTUIModel(app App, remote string, port int) tuiModel {
	m := tuiModel{
		app:     app,
		remote:  remote,
		port:    port,
		width:   80,
		height:  24,
		message: initialTUIMessage(app.UploadEnabled()),
	}
	m.loadEntries()
	return m
}

func (m tuiModel) Init() tea.Cmd {
	return tuiTick()
}

func tuiTick() tea.Cmd {
	return tea.Tick(160*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.tick++
		return m, tuiTick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.mode != tuiModeBrowse {
			updated, cmd := m.updateUploadInput(msg)
			return updated, cmd
		}
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= 10
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += 10
			if m.cursor >= len(m.entries) {
				m.cursor = len(m.entries) - 1
			}
		case "backspace", "left", "h", "b":
			m.goParent()
		case "r":
			m.copyCommand = ""
			m.loadEntries()
			m.message = "Refreshed."
		case "enter", "right", "l":
			m.copyCommand = ""
			m.openSelected()
		case "d":
			m.copyCommand = ""
			return m, m.downloadSelected()
		case "u":
			m.startUploadHelper()
		}
	}
	return m, nil
}

func initialTUIMessage(allowUpload bool) string {
	if allowUpload {
		return "Use ↑/↓ or j/k, Enter to open/view, D download, U scp upload helper."
	}
	return "Use ↑/↓ or j/k, Enter to open/view, D for scp download command."
}

func (m tuiModel) updateUploadInput(msg tea.KeyMsg) (tuiModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = tuiModeBrowse
		m.input = ""
		m.copyCommand = ""
		m.message = "Upload cancelled."
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "enter":
		return m.finishUploadInput()
	default:
		if msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m tuiModel) finishUploadInput() (tuiModel, tea.Cmd) {
	switch m.mode {
	case tuiModeUploadPath:
		localPath := strings.TrimSpace(m.input)
		if localPath == "" {
			m.message = "Paste or drag a local file path first. Esc cancels."
			return m, nil
		}
		m.uploadLocalPath = localPath
		m.uploadRemoteName = clientPathBase(localPath)
		m.input = ""
		m.mode = tuiModeUploadName
		m.message = "Server filename: press Enter for same name, or type a new name."
	case tuiModeUploadName:
		name := strings.TrimSpace(m.input)
		if name == "" {
			name = m.uploadRemoteName
		}
		if !isPlainFilename(name) {
			m.message = "Server filename must be a plain filename, not a path."
			return m, nil
		}
		command := scpUploadCommand(m.port, m.remote, m.uploadLocalPath, name)
		m.copyCommand = command
		m.message = "SCP command copied if your terminal allows it. Copy/run this one line:"
		m.mode = tuiModeBrowse
		m.input = ""
		return m, osc52CopyCmd(command)
	}
	return m, nil
}

func (m *tuiModel) loadEntries() {
	p, err := m.app.ResolvePath(m.relDir)
	if err != nil {
		m.lastError = err.Error()
		m.entries = nil
		m.cursor = 0
		return
	}
	items, err := os.ReadDir(p)
	if err != nil {
		m.lastError = err.Error()
		m.entries = nil
		m.cursor = 0
		return
	}
	entries := make([]tuiEntry, 0, len(items))
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			continue
		}
		entries = append(entries, tuiEntry{Name: item.Name(), IsDir: item.IsDir(), Size: info.Size()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	m.entries = entries
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.lastError = ""
}

func (m *tuiModel) selected() (tuiEntry, bool) {
	if len(m.entries) == 0 || m.cursor < 0 || m.cursor >= len(m.entries) {
		return tuiEntry{}, false
	}
	return m.entries[m.cursor], true
}

func (m *tuiModel) openSelected() {
	entry, ok := m.selected()
	if !ok {
		m.message = "No file selected."
		return
	}
	if entry.IsDir {
		m.relDir = filepath.ToSlash(filepath.Join(m.relDir, entry.Name))
		m.cursor = 0
		m.viewer = ""
		m.loadEntries()
		m.message = "Opened " + entry.Name + "/"
		return
	}
	path := filepath.ToSlash(filepath.Join(m.relDir, entry.Name))
	p, err := m.app.ResolvePath(path)
	if err != nil {
		m.message = err.Error()
		return
	}
	f, err := os.Open(p)
	if err != nil {
		m.message = err.Error()
		return
	}
	defer f.Close()
	buf := make([]byte, 2048)
	n, _ := f.Read(buf)
	m.viewer = string(buf[:n])
	if len(m.viewer) == 0 {
		m.viewer = "(empty file)"
	}
	m.message = "Preview: " + path
}

func (m *tuiModel) downloadSelected() tea.Cmd {
	entry, ok := m.selected()
	if !ok {
		m.message = "No file selected."
		return nil
	}
	path := filepath.ToSlash(filepath.Join(m.relDir, entry.Name))
	command := scpDownloadCommand(m.port, m.remote, path, entry.IsDir)
	m.copyCommand = command
	m.message = "SCP download command copied if your terminal allows it. Copy/run this one line on your computer:"
	return osc52CopyCmd(command)
}

func (m *tuiModel) startUploadHelper() {
	if !m.app.UploadEnabled() {
		m.copyCommand = ""
		m.message = "Upload is disabled. Restart the server with -upload <directory> to enable scp uploads."
		return
	}
	m.mode = tuiModeUploadPath
	m.input = ""
	m.uploadLocalPath = ""
	m.uploadRemoteName = ""
	m.copyCommand = ""
	m.message = "Paste or drag a local file path here, then press Enter."
}

func (m *tuiModel) goParent() {
	if m.relDir == "" || m.relDir == "." {
		m.message = "Already at the download root."
		return
	}
	m.relDir = filepath.ToSlash(filepath.Dir(m.relDir))
	if m.relDir == "." {
		m.relDir = ""
	}
	m.cursor = 0
	m.viewer = ""
	m.loadEntries()
	m.message = "Moved up."
}

func shellQuoteInsideSingle(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func scpUploadCommand(port int, remote, localPath, serverName string) string {
	remoteSpec := fmt.Sprintf("%s:%s", remote, serverName)
	return fmt.Sprintf("scp -O -r -P %d %s %s", port, shellQuoteArg(localPath), shellQuoteArg(remoteSpec))
}

func scpDownloadCommand(port int, remote, serverPath string, recursive bool) string {
	remoteSpec := fmt.Sprintf("%s:%s", remote, serverPath)
	if recursive {
		return fmt.Sprintf("scp -O -r -P %d %s .", port, shellQuoteArg(remoteSpec))
	}
	return fmt.Sprintf("scp -O -P %d %s .", port, shellQuoteArg(remoteSpec))
}

func osc52CopyCmd(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	return tea.Exec(&terminalWriteCommand{text: seq}, nil)
}

type terminalWriteCommand struct {
	text string
	out  io.Writer
}

func (c *terminalWriteCommand) Run() error {
	if c.out == nil {
		return nil
	}
	_, err := io.WriteString(c.out, c.text)
	return err
}

func (c *terminalWriteCommand) SetStdin(io.Reader)    {}
func (c *terminalWriteCommand) SetStdout(w io.Writer) { c.out = w }
func (c *terminalWriteCommand) SetStderr(io.Writer)   {}

func shellQuoteArg(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func clientPathBase(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "\"'")
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (m tuiModel) View() string {
	spin := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinner := spin[m.tick%len(spin)]
	cwd := "/"
	if m.relDir != "" {
		cwd = "/" + m.relDir
	}

	title := titleStyle.Render("SSDOV " + spinner)
	pathLine := subtleStyle.Render("root " + cwd)
	body := m.renderEntries()
	if m.viewer != "" {
		body += "\n" + previewStyle.Render(truncateLines(m.viewer, 8))
	}
	if m.lastError != "" {
		body += "\n" + errorStyle.Render(m.lastError)
	}
	if m.mode != tuiModeBrowse {
		body += "\n" + uploadInputStyle.Render(m.uploadPrompt()+"\n"+m.input)
	}
	msgWidth := max(40, m.width-4)
	appWidth := max(50, m.width-2)
	if m.copyCommand != "" {
		commandWidth := lipgloss.Width(m.copyCommand) + 4
		msgWidth = max(msgWidth, commandWidth)
		appWidth = max(appWidth, commandWidth)
	}
	msg := messageStyle.Width(msgWidth).Render(m.message)
	buttons := renderButtons(m.app.UploadEnabled())

	parts := []string{title, pathLine, "", body, "", msg}
	if m.copyCommand != "" {
		parts = append(parts, m.copyCommand)
	}
	parts = append(parts, buttons)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return appStyle.Width(appWidth).Render(content)
}

func (m tuiModel) renderEntries() string {
	if len(m.entries) == 0 {
		return emptyStyle.Render("No files here.")
	}
	maxRows := max(5, m.height-12)
	start := 0
	if m.cursor >= maxRows {
		start = m.cursor - maxRows + 1
	}
	end := min(len(m.entries), start+maxRows)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		e := m.entries[i]
		icon := "📄"
		name := e.Name
		meta := humanSize(e.Size)
		if e.IsDir {
			icon = "📁"
			name += "/"
			meta = "folder"
		}
		line := fmt.Sprintf("%s  %-32s %8s", icon, name, meta)
		if i == m.cursor {
			line = selectedStyle.Render("› " + line)
		} else {
			line = normalStyle.Render("  " + line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderButtons(allowUpload bool) string {
	buttons := []string{
		buttonStyle.Render("[ Enter Open/View ]"),
		buttonStyle.Render("[ D Download ]"),
	}
	if allowUpload {
		buttons = append(buttons, buttonStyle.Render("[ U Upload ]"))
	}
	buttons = append(buttons,
		buttonStyle.Render("[ B Back ]"),
		buttonStyle.Render("[ R Refresh ]"),
		dangerButtonStyle.Render("[ Q Quit ]"),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, buttons...)
}

func (m tuiModel) uploadPrompt() string {
	switch m.mode {
	case tuiModeUploadPath:
		return "Local file path (drag/paste here, Enter):"
	case tuiModeUploadName:
		return fmt.Sprintf("Server filename (Enter = %s):", m.uploadRemoteName)
	default:
		return ""
	}
}

func truncateLines(s string, maxLines int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "…")
	}
	return strings.Join(lines, "\n")
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n >= div*unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	appStyle = lipgloss.NewStyle().
			Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("62")).
			Bold(true).
			Padding(0, 1)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Padding(0, 1)

	buttonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("63")).
			Bold(true).
			MarginRight(1)

	dangerButtonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("160")).
				Bold(true).
				MarginRight(1)

	messageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	uploadInputStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("70")).
				Foreground(lipgloss.Color("229")).
				Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true)
)
