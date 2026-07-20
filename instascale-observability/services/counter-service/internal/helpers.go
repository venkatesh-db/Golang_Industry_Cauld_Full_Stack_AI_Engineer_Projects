package counter

import "strconv"

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// toInt parses a Redis string value to int64, returning 0 on any error.
func toInt(v any) int64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
