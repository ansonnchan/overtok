package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func (m Model) render() string {
	width := m.width
	if width <= 0 {
		width = wideWidth
	}
	if width < MinimumWidth {
		lines := []string{
			m.styles.title.Render("Overtok"),
			"",
			m.styles.warning.Render("[warning] Terminal is too narrow"),
			fmt.Sprintf("Resize to at least %d columns.", MinimumWidth),
			"",
			"q quit · ? help",
		}
		if m.help {
			lines = append(lines, "", "1–4 views · r refresh · q quit")
		}
		return strings.Join(lines, "\n") + "\n"
	}

	var output strings.Builder
	fmt.Fprintln(&output, m.styles.title.Render("Overtok · local usage"))
	fmt.Fprintln(&output, m.renderNavigation(width))
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, m.styles.title.Render(sectionName(m.section)))
	if m.help {
		m.renderHelp(&output, width)
	} else {
		m.renderContent(&output, width)
	}
	fmt.Fprintln(&output)
	footer := "1–4 views · tab navigate · r refresh · ? help · q quit"
	if width < wideWidth {
		footer = "1–4 views · r refresh · ? help · q quit"
	}
	if m.refreshing {
		footer = "[loading] Refreshing local data · " + footer
	}
	fmt.Fprintln(&output, m.styles.muted.Render(clip(footer, width)))
	return output.String()
}

func (m Model) renderNavigation(width int) string {
	labels := make([]string, 0, len(sections))
	for index, section := range sections {
		label := fmt.Sprintf("%d %s", index+1, sectionName(section))
		if section == m.section {
			label = m.styles.selected.Render("[" + label + "]")
		}
		labels = append(labels, label)
	}
	if width >= wideWidth {
		return strings.Join(labels, "    ")
	}
	return strings.Join(labels, "  ")
}

func (m Model) renderContent(output *strings.Builder, width int) {
	switch m.state {
	case StateLoading:
		fmt.Fprintln(output, m.styles.muted.Render("[loading] Reading committed local data…"))
	case StateEmpty:
		fmt.Fprintln(output, m.styles.muted.Render("[empty] No local data to show yet."))
		fmt.Fprintln(output, "Press r to collect and refresh.")
	case StateWarning:
		for _, line := range wrap("[warning] "+cleanLine(m.page.Warning), width) {
			fmt.Fprintln(output, m.styles.warning.Render(line))
		}
		m.renderLines(output, width)
	case StateError:
		message := m.errMessage
		if message == "" {
			message = "Could not load local data"
		}
		for _, line := range wrap("[error] "+message+".", width) {
			fmt.Fprintln(output, m.styles.failure.Render(line))
		}
		for _, line := range wrap("Press r to retry or run `overtok doctor` for details.", width) {
			fmt.Fprintln(output, line)
		}
	default:
		fmt.Fprintln(output, m.styles.success.Render("[ready] Local data loaded"))
		m.renderLines(output, width)
	}
}

func (m Model) renderLines(output *strings.Builder, width int) {
	for _, line := range m.page.Lines {
		for _, wrapped := range wrap(cleanLine(line), width) {
			fmt.Fprintln(output, wrapped)
		}
	}
}

func (m Model) renderHelp(output *strings.Builder, width int) {
	lines := []string{
		"1–4        switch view",
		"tab/arrows move between views",
		"r          refresh local data",
		"?          close help",
		"q          quit",
	}
	if width < wideWidth {
		lines[1] = "tab/arrows navigate"
	}
	for _, line := range lines {
		fmt.Fprintln(output, clip(line, width))
	}
}

func clip(value string, width int) string {
	if width <= 0 || utf8.RuneCountInString(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func wrap(value string, width int) []string {
	if value == "" || width <= 0 {
		return []string{value}
	}
	var words []string
	for _, word := range strings.Fields(value) {
		for utf8.RuneCountInString(word) > width {
			runes := []rune(word)
			words = append(words, string(runes[:width]))
			word = string(runes[width:])
		}
		if word != "" {
			words = append(words, word)
		}
	}
	if len(words) == 0 {
		return []string{""}
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		candidate := lines[last] + " " + word
		if utf8.RuneCountInString(candidate) <= width {
			lines[last] = candidate
			continue
		}
		lines = append(lines, word)
	}
	return lines
}
