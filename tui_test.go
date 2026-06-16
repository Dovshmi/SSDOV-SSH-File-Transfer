package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIViewLooksModernAndShowsButtons(t *testing.T) {
	app := makeTestApp(t)
	m := newTUIModel(app, "download@server", 2222)
	view := m.View()

	for _, want := range []string{
		"SSDOV",
		"hello.txt",
		"[ Enter Open/View ]",
		"[ D Download ]",
		"[ Q Quit ]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("TUI view missing %q:\n%s", want, view)
		}
	}
}

func TestTUISelectionMovesDownAndUp(t *testing.T) {
	app := makeTestApp(t)
	m := newTUIModel(app, "download@server", 2222)
	if m.cursor != 0 {
		t.Fatalf("initial cursor=%d, want 0", m.cursor)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(tuiModel)
	if m.cursor != 1 {
		t.Fatalf("cursor after down=%d, want 1", m.cursor)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(tuiModel)
	if m.cursor != 0 {
		t.Fatalf("cursor after up=%d, want 0", m.cursor)
	}
}

func TestTUIDownloadHintUsesSelectedFile(t *testing.T) {
	app := makeTestApp(t)
	m := newTUIModel(app, "download@nas", 2222)
	m.cursor = indexOfEntry(t, m.entries, "hello.txt")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = next.(tuiModel)

	for _, want := range []string{"ssh -p 2222 download@nas", "download hello.txt", "> hello.txt"} {
		if !strings.Contains(m.message, want) {
			t.Fatalf("download hint missing %q: %q", want, m.message)
		}
	}
}

func TestTUIEnterOpensDirectory(t *testing.T) {
	app := makeTestApp(t)
	m := newTUIModel(app, "download@server", 2222)
	m.cursor = indexOfEntry(t, m.entries, "docs")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(tuiModel)

	if m.relDir != "docs" {
		t.Fatalf("relDir=%q, want docs", m.relDir)
	}
	if indexOfEntry(t, m.entries, "readme.md") < 0 {
		t.Fatalf("docs directory entries do not include readme.md: %#v", m.entries)
	}
}

func indexOfEntry(t *testing.T, entries []tuiEntry, name string) int {
	t.Helper()
	for i, e := range entries {
		if e.Name == name {
			return i
		}
	}
	t.Fatalf("entry %q not found in %#v", name, entries)
	return -1
}
