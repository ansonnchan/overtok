package tui

import "github.com/charmbracelet/lipgloss"

type styleSet struct {
	title    lipgloss.Style
	selected lipgloss.Style
	muted    lipgloss.Style
	success  lipgloss.Style
	warning  lipgloss.Style
	failure  lipgloss.Style
}

func newStyles(noColor bool) styleSet {
	if noColor {
		return styleSet{}
	}
	return styleSet{
		title:    lipgloss.NewStyle().Bold(true),
		selected: lipgloss.NewStyle().Foreground(lipgloss.Color("209")).Bold(true),
		muted:    lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		success:  lipgloss.NewStyle().Foreground(lipgloss.Color("71")),
		warning:  lipgloss.NewStyle().Foreground(lipgloss.Color("179")),
		failure:  lipgloss.NewStyle().Foreground(lipgloss.Color("167")),
	}
}
