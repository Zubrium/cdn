package config

import (
	"fmt"
	"os"
)

// validateWritableTempDir preflights the operator-configured directory during
// startup configuration loading. It creates the directory if necessary and
// proves the process can create and remove a private temporary file there.
func validateWritableTempDir(setting, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s must name a writable temporary directory", setting)
	}
	file, err := os.CreateTemp(dir, ".website-engine-config-check-*")
	if err != nil {
		return fmt.Errorf("%s must name a writable temporary directory", setting)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("%s temporary file could not be closed", setting)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%s temporary file could not be removed", setting)
	}
	return nil
}
