package synthesizer

import (
	"os"
	"strings"
)

func resolveEnvironmentReference(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "${") || !strings.HasSuffix(trimmed, "}") {
		return trimmed
	}

	name := strings.TrimSpace(trimmed[2 : len(trimmed)-1])
	if name == "" {
		return ""
	}
	resolved, ok := os.LookupEnv(name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(resolved)
}
