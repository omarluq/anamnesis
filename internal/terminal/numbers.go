package terminal

// Layout constants ported from librecode's terminal package, governing the
// padding and row budgets of the transcript message renderers.
const (
	// messageHorizontalPadding is the two-space gutter on each side of a message.
	messageHorizontalPadding = 2
	// messageBoxHorizontalPadding is the combined left+right gutter width.
	messageBoxHorizontalPadding = messageHorizontalPadding * 2
	// messageOuterRows counts the blank spacer rows a message reserves above and
	// below its body.
	messageOuterRows = 2
	// messageMetadataRows counts the spacer-plus-label rows a thinking block
	// reserves around its content.
	messageMetadataRows = 3
	// defaultMessageExtraRows is the slice headroom a boxed user message reserves.
	defaultMessageExtraRows = 4
	// maxCollapsedQueryOutputLines caps how many trailing output lines a collapsed
	// query block previews before it elides the rest behind the expand hint.
	maxCollapsedQueryOutputLines = 5
	// initialQueryBlockLines is the slice headroom an expanded query block reserves.
	initialQueryBlockLines = 10
	// footerRows is the height of the status footer at the bottom of the shell.
	footerRows = 1
	// composerBodyRows is the editable body height of the composer, excluding its
	// border rows.
	composerBodyRows = 4
	// composerBorders counts the composer's top and bottom border rows.
	composerBorders = 2
)
