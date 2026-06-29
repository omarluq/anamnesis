package transcript

// Role identifies a transcript message category independent from storage roles.
type Role string

const (
	// RoleUser is a user-authored prompt.
	RoleUser Role = "user"
	// RoleAssistant is an assistant response.
	RoleAssistant Role = "assistant"
	// RoleToolResult is output from a tool execution.
	RoleToolResult Role = "toolResult"
	// RoleThinking is model reasoning or thinking text.
	RoleThinking Role = "thinking"
	// RoleBashExecution is output from a user-run shell command.
	RoleBashExecution Role = "bashExecution"
)
