package runtime

import (
	"fmt"
	"strings"
)

func validSource(s string) bool { return s == "auto" || s == "meow" || s == "spectre" }

func sourcesFor(source string) []string {
	switch source {
	case "meow":
		return []string{"meow"}
	case "spectre":
		return []string{"spectre"}
	default:
		return []string{"meow", "spectre"}
	}
}
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
