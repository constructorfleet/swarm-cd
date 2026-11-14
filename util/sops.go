package util

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/getsops/sops/v3/decrypt"
)

func DecryptFile(filepath string) (err error) {
	format := getFileFormat(filepath)
	textBytes, err := decrypt.File(filepath, format)
	if err != nil {
		return fmt.Errorf("could not decrypt the file %s: %w", filepath, err)
	}
	err = os.WriteFile(filepath, textBytes, 0600)
	if err != nil {
		return fmt.Errorf("could not write the file %s: %w", filepath, err)
	}
	return
}

func getFileFormat(filename string) string {
	switch filepath.Ext(filename) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".ini":
		return "ini"
	case ".env":
		return "dotenv"
	default:
		return "binary"
	}
}
