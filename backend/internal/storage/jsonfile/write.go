package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces a JSON file after the new contents have been flushed to disk.
func Write(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("create state file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("set state file mode: %w", err)
	}
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush state file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
