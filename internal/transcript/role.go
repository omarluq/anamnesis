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
	// RoleCustom is extension-provided context.
	RoleCustom Role = "custom"
	// RoleBashExecution is output from a user-run shell command.
	RoleBashExecution Role = "bashExecution"
	// RoleBranchSummary is summary context for an abandoned branch.
	RoleBranchSummary Role = "branchSummary"
	// RoleCompactionSummary is summary context for compacted history.
	RoleCompactionSummary Role = "compactionSummary"
)
