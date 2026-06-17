package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const startupServiceName = "ssdov.service"

func installStartupService(cfg serverConfig) (string, error) {
	binary, err := os.Executable()
	if err != nil || binary == "" {
		binary = os.Args[0]
	}
	path, err := writeStartupService(cfg, binary)
	if err != nil {
		return "", err
	}
	if err := runSystemctlUser("daemon-reload"); err != nil {
		return path, err
	}
	if err := runSystemctlUser("enable", startupServiceName); err != nil {
		return path, err
	}
	return path, nil
}

func writeStartupService(cfg serverConfig, binaryPath string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	unitDir := filepath.Join(configDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(unitDir, startupServiceName)
	content := buildStartupService(cfg, binaryPath)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func runSystemctlUser(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func buildStartupService(cfg serverConfig, binaryPath string) string {
	args := []string{
		systemdQuoteArg(binaryPath),
		"-addr", systemdQuoteArg(cfg.Addr),
		"-root", systemdQuoteArg(cfg.Root),
		"-user", systemdQuoteArg(cfg.User),
		"-host-key", systemdQuoteArg(cfg.HostKey),
	}
	if normalizeUploadDir(cfg.UploadDir) != "" {
		args = append(args, "-upload", systemdQuoteArg(cfg.UploadDir))
	}

	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=SSDOV SSH file server\n")
	b.WriteString("After=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if cfg.Password != "" {
		b.WriteString("Environment=SSHDOWN_PASSWORD=")
		b.WriteString(systemdQuoteArg(cfg.Password))
		b.WriteString("\n")
	}
	b.WriteString("ExecStart=")
	b.WriteString(strings.Join(args, " "))
	b.WriteString("\n")
	b.WriteString("Restart=on-failure\n")
	b.WriteString("RestartSec=3\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

func systemdQuoteArg(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "%", "%%")
	return "\"" + s + "\""
}
