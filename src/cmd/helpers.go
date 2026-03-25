package cmd

import (
	"path/filepath"

	"github.com/kenbolton/molt/src/driver"
)

func locateDriver(arch string, sourceDir ...string) (*driver.Driver, error) {
	return driver.Locate(arch, sourceDir...)
}

func detectOrFlagArch(sourceDir string) (string, error) {
	if flagArch != "" {
		return flagArch, nil
	}
	return driver.DetectArch(sourceDir)
}

func bundleNameFromSource(sourceDir string) string {
	base := filepath.Base(filepath.Clean(sourceDir))
	return base + ".molt"
}
