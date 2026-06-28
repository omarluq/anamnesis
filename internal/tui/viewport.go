package tui

// SliceViewport returns the visible range for offset and height.
func SliceViewport[T any](items []T, offset, height int) []T {
	if height <= 0 || len(items) == 0 {
		return []T{}
	}

	start := min(max(0, offset), max(0, len(items)-height))
	end := min(start+height, len(items))

	return items[start:end]
}
