package components

import "github.com/charmbracelet/lipgloss"

// --- Pulso palette (petroleum, turquoise and amber) ---
var (
	colorPrimary   = lipgloss.Color("#42C5B6") // turquoise
	colorSecondary = lipgloss.Color("#87AAA7") // gray
	colorSuccess   = lipgloss.Color("#7DCEAB") // bright green
	colorWarning   = lipgloss.Color("#F1B963") // bright yellow
	colorError     = lipgloss.Color("#F08B8E") // bright red
	colorHighlight = lipgloss.Color("#F1B963") // amber
	colorDim       = lipgloss.Color("#8AA6A9") // slate
	colorInfo      = lipgloss.Color("#83CBD3") // mist
	colorMuted     = lipgloss.Color("#AEC6C5") // muted mist
	colorSurface   = lipgloss.Color("#123D43") // petroleum
)

// --- Title bar ---
var TitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#123D43")).
	Background(colorPrimary).
	Padding(0, 2)

// --- Status bar ---
var StatusStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#EFF8F5")).
	Background(colorSecondary).
	Padding(0, 1)

// --- Process state styles ---
var (
	StateRunning  = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	StateStopped  = lipgloss.NewStyle().Foreground(colorError)
	StateCrashed  = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	StateStarting = lipgloss.NewStyle().Foreground(colorInfo)
	StateStopping = lipgloss.NewStyle().Foreground(colorWarning)
	StateWaiting  = lipgloss.NewStyle().Foreground(colorWarning)
	StateErrored  = lipgloss.NewStyle().Foreground(colorError)
	StateDefault  = lipgloss.NewStyle().Foreground(colorSecondary)
)

// --- Text styles ---
var (
	HelpStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Padding(0, 2)

	DimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	MutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)
)

// --- Table styles ---
var SelectedStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#28575C")).
	Foreground(lipgloss.Color("#EFF8F5"))

// --- Log area ---
var LogStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.RoundedBorder()).
	BorderForeground(colorSecondary).
	Padding(0, 1)

// --- Inspect TUI styles ---
var (
	InspectHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				BorderBottom(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(colorDim).
				MarginBottom(1)

	InspectLabelStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Width(14)

	InspectValueStyle = lipgloss.NewStyle().
				Bold(true)

	InspectAccentStyle = lipgloss.NewStyle().
				Foreground(colorHighlight)

	SectionBorderStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(colorDim).
				Padding(0, 1)

	ActionActiveStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	ActionDimStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

// --- Separator ---
var SeparatorStyle = lipgloss.NewStyle().
	Foreground(colorSurface)

// --- Empty state ---
var EmptyStyle = lipgloss.NewStyle().
	Foreground(colorMuted).
	Italic(true).
	Padding(1, 2)

// --- Badge (inline state label) ---
var (
	BadgeRunning = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	BadgeStopped = lipgloss.NewStyle().
			Foreground(colorError)

	BadgeCrashed = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	BadgeStarting = lipgloss.NewStyle().
			Foreground(colorInfo)

	BadgeStopping = lipgloss.NewStyle().
			Foreground(colorWarning)

	BadgeWaiting = lipgloss.NewStyle().
			Foreground(colorWarning)

	BadgeErrored = lipgloss.NewStyle().
			Foreground(colorError)
)

// StateStyle returns the lipgloss style for a process state string.
func StateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return StateRunning
	case "stopped":
		return StateStopped
	case "crashed":
		return StateCrashed
	case "starting":
		return StateStarting
	case "stopping":
		return StateStopping
	case "waiting":
		return StateWaiting
	case "errored":
		return StateErrored
	default:
		return StateDefault
	}
}

// BadgeStyle returns a compact badge style for a process state.
func BadgeStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return BadgeRunning
	case "stopped":
		return BadgeStopped
	case "crashed":
		return BadgeCrashed
	case "starting":
		return BadgeStarting
	case "stopping":
		return BadgeStopping
	case "waiting":
		return BadgeWaiting
	case "errored":
		return BadgeErrored
	default:
		return StateDefault
	}
}
