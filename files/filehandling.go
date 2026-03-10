package files

import (
	"errors"
	"os"
)

// FileExists checks if a file or directory exists.
func FileExists(filename string) bool {
	_, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Other errors (e.g., permission, disk failure) might occur.
	// If err is nil, the file or directory exists.
	return err == nil
}
