package size

import (
	"fmt"
	"strconv"
	"strings"
)

func FormatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func FormatNumber(size int64) string {
	const unit = 1000
	if size < unit {
		return fmt.Sprintf("%d", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(size)/float64(div), "kMGTPE"[exp])
}

func MustParse(size string) int64 {
	parsedSize, err := Parse(size)
	if err != nil {
		panic(err)
	}
	return parsedSize
}

func Parse(size string) (int64, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 0, nil
	}

	// Convert to lowercase for comparison but keep original for parsing
	sizeLower := strings.ToLower(size)

	multipliers := map[string]int64{
		"b":   1,
		"k":   1024,
		"kb":  1024,
		"kib": 1024,
		"m":   1024 * 1024,
		"mb":  1024 * 1024,
		"mib": 1024 * 1024,
		"g":   1024 * 1024 * 1024,
		"gb":  1024 * 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"t":   1024 * 1024 * 1024 * 1024,
		"tb":  1024 * 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
	}

	// Check suffixes from longest to shortest to avoid "g" matching "gb"
	suffixes := []string{"tib", "gib", "mib", "kib", "tb", "gb", "mb", "kb", "t", "g", "m", "k", "b"}

	for _, suffix := range suffixes {
		if strings.HasSuffix(sizeLower, suffix) {
			numStr := size[:len(size)-len(suffix)]
			numStr = strings.TrimSpace(numStr)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number: %s", numStr)
			}
			return int64(num * float64(multipliers[suffix])), nil
		}
	}

	num, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size: %s", size)
	}

	return num, nil
}
