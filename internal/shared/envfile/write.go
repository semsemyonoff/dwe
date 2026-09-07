package envfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Write renders the .env content from cfg and writes it to outputPath,
// creating parent directories as needed. baseDir is the project root, used to
// resolve the compose project name (see BuildContent).
func Write(cfg *config.DweConfig, baseDir, outputPath string) error {
	content, err := BuildContent(cfg, baseDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outputPath), err)
	}
	// os.WriteFile keeps the mode of a pre-existing file, so a .env created
	// before dwe (or by a laxer umask) would stay world-readable while holding
	// decrypted secrets. Tighten it explicitly, and do it BEFORE the write so
	// the plaintext is never on disk at the looser mode. A missing file is not
	// an error here — WriteFile creates it at 0600.
	if err := os.Chmod(outputPath, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("chmod %s: %w", outputPath, err)
	}
	if err := os.WriteFile(outputPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}

// Regenerate reloads config from configPath, writes .env next to it,
// and returns the absolute path of the written file.
func Regenerate(configPath string) (string, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", fmt.Errorf("reload config: %w", err)
	}
	baseDir := filepath.Dir(configPath)
	envPath := filepath.Join(baseDir, ".env")
	if err := Write(cfg, baseDir, envPath); err != nil {
		return "", err
	}
	return envPath, nil
}
