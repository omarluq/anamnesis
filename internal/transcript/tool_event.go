// Package transcript contains provider-neutral transcript formatting helpers shared by
// assistant persistence and terminal presentation.
package transcript

import "strings"

// ToolEvent captures the display fields of a completed tool call.
type ToolEvent struct {
	Name          string
	ArgumentsJSON string
	DetailsJSON   string
	Result        string
	Error         string
}

// FormatToolEventDisplay formats a tool event for terminal display.
func FormatToolEventDisplay(event *ToolEvent) string {
	if event == nil {
		return "tool: "
	}

	parts := []string{"tool: " + event.Name}
	if strings.TrimSpace(event.ArgumentsJSON) != "" {
		parts = append(parts, "arguments:", event.ArgumentsJSON)
	}

	if event.Error != "" {
		parts = append(parts, "error:", event.Error)
	}

	if strings.TrimSpace(event.DetailsJSON) != "" {
		parts = append(parts, "details:", event.DetailsJSON)
	}

	if strings.TrimSpace(event.Result) != "" {
		parts = append(parts, "output:", event.Result)
	}

	return strings.Join(parts, "\n")
}
