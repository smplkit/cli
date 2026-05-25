package values

import (
	"fmt"
	"os"
	"strings"
)

// AtFileOrLiteral returns the file contents if raw starts with "@", or
// the literal string otherwise. Used wherever a CLI flag accepts
// either an inline JSON/JSONata literal or a `@path/to/file` reference.
func AtFileOrLiteral(raw string) (string, error) {
	if strings.HasPrefix(raw, "@") {
		path := strings.TrimPrefix(raw, "@")
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		return string(data), nil
	}
	return raw, nil
}
