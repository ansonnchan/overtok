package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeApplication struct {
	pages      map[Section]Page
	loadError  error
	refreshErr error
	loads      []Section
	refreshes  int
}

func (a *fakeApplication) Load(_ context.Context, section Section) (Page, error) {
	a.loads = append(a.loads, section)
	return a.pages[section], a.loadError
}

func (a *fakeApplication) Refresh(context.Context) error {
	a.refreshes++
	return a.refreshErr
}

func TestModelLoadsAndNavigatesWithKeyboard(t *testing.T) {
	t.Parallel()
	app := &fakeApplication{pages: map[Section]Page{
		OverviewSection: {Lines: []string{"Today 42 tokens"}},
		SourcesSection:  {Warning: "Codex needs attention", Lines: []string{"Codex: degraded"}},
	}}
	model := NewModel(t.Context(), app, Config{NoColor: true})
	message := model.Init()()
	updated, command := model.Update(message)
	model = updated.(Model)
	if command != nil || model.state != StateReady || !strings.Contains(model.View(), "Today 42 tokens") {
		t.Fatalf("initial model = %#v view=%q", model, model.View())
	}

	updated, command = model.Update(key('2'))
	model = updated.(Model)
	if model.section != SourcesSection || model.state != StateLoading || command == nil {
		t.Fatalf("navigation model = %#v", model)
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.state != StateWarning || !strings.Contains(model.View(), "[warning] Codex needs attention") {
		t.Fatalf("warning model = %#v view=%q", model, model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := updated.(Model).section; got != ModelsSection {
		t.Fatalf("tab section = %v, want models", got)
	}
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := updated.(Model).section; got != SourcesSection {
		t.Fatalf("shift-tab section = %v, want sources", got)
	}
}

func TestModelIgnoresStaleLoadsAndRefreshesThroughApplication(t *testing.T) {
	t.Parallel()
	app := &fakeApplication{pages: map[Section]Page{OverviewSection: {Lines: []string{"fresh"}}}}
	model := NewModel(t.Context(), app, Config{NoColor: true})
	updated, _ := model.Update(key('2'))
	model = updated.(Model)
	updated, command := model.Update(loadResult{section: OverviewSection, page: Page{Lines: []string{"stale"}}})
	model = updated.(Model)
	if command != nil || strings.Contains(model.View(), "stale") || model.section != SourcesSection {
		t.Fatalf("stale result changed model = %#v", model)
	}

	updated, command = model.Update(key('r'))
	model = updated.(Model)
	if command == nil || !model.refreshing {
		t.Fatalf("refresh did not start = %#v", model)
	}
	updated, command = model.Update(command())
	model = updated.(Model)
	if app.refreshes != 1 || command == nil {
		t.Fatalf("refresh result = %#v calls=%d", model, app.refreshes)
	}
}

func TestModelStatesRemainExplicitWithoutColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message loadResult
		want    string
	}{
		{name: "empty", message: loadResult{section: OverviewSection}, want: "[empty]"},
		{name: "error", message: loadResult{section: OverviewSection, err: errors.New("private detail")}, want: "[error]"},
		{name: "warning", message: loadResult{section: OverviewSection, page: Page{Warning: "partial"}}, want: "[warning]"},
		{name: "ready", message: loadResult{section: OverviewSection, page: Page{Lines: []string{"value"}}}, want: "[ready]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(t.Context(), &fakeApplication{}, Config{NoColor: true})
			updated, _ := model.Update(test.message)
			view := updated.(Model).View()
			if !strings.Contains(view, test.want) || strings.Contains(view, "\x1b[") || strings.Contains(view, "private detail") {
				t.Fatalf("View() = %q", view)
			}
		})
	}
}

func TestNarrowLayoutAndHelp(t *testing.T) {
	t.Parallel()
	model := NewModel(t.Context(), nil, Config{NoColor: true})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: MinimumWidth - 1, Height: 20})
	view := updated.(Model).View()
	if !strings.Contains(view, "Terminal is too narrow") || !strings.Contains(view, "48 columns") {
		t.Fatalf("too-narrow View() = %q", view)
	}
	updated, _ = updated.(Model).Update(key('?'))
	if view = updated.(Model).View(); !strings.Contains(view, "1–4 views") {
		t.Fatalf("too-narrow help View() = %q", view)
	}

	updated, _ = updated.(Model).Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	view = updated.(Model).View()
	if !strings.Contains(view, "tab/arrows navigate") || strings.Contains(view, "move between views") {
		t.Fatalf("narrow help View() = %q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 60 {
			t.Fatalf("narrow line is %d columns: %q", len([]rune(line)), line)
		}
	}
}

func TestRefreshFailureIsActionable(t *testing.T) {
	t.Parallel()
	app := &fakeApplication{refreshErr: errors.New("private path")}
	model := NewModel(t.Context(), app, Config{NoColor: true})
	updated, command := model.Update(key('r'))
	updated, command = updated.(Model).Update(command())
	if command != nil {
		t.Fatal("failed refresh returned another command")
	}
	view := updated.(Model).View()
	if !strings.Contains(view, "Could not refresh local data") || strings.Contains(view, "private path") {
		t.Fatalf("refresh failure View() = %q", view)
	}
}

func key(value rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}}
}
