package runtime

import (
	"fmt"
	"strings"
)

func parseSources(value string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range strings.Fields(value) {
		switch s {
		case "meow", "spectre", "addon":
		default:
			return nil, fmt.Errorf("invalid KBL_SOURCES source: %s", s)
		}
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("KBL_SOURCES is empty")
	}
	return out, nil
}
