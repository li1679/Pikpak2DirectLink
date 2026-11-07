package app

import (
	"fmt"
	"strconv"
)

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// formatTrafficLabel uses binary units, matching PikPak file sizes and account limits.
func formatTrafficLabel(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(b)
	i := -1
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	if value >= 100 || value == float64(int64(value)) {
		return fmt.Sprintf("%.0f %s", value, units[i])
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}

func itoa(n int) string { return strconv.Itoa(n) }
