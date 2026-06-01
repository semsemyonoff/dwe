package envfile

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Write renders the .env content from cfg and writes it to outputPath,
// creating parent directories as needed.
func Write(cfg *config.DevboxConfig, outputPath string) error {
	content, err := BuildContent(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outputPath), err)
	}
	if err := os.WriteFile(outputPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}

// Regenerate reloads config from configPath, writes .env next to it,
// and returns the absolute path of the written file.
// It replaces regenEnv(configPath, baseDir) — baseDir is implied by configPath's directory.
func Regenerate(configPath string) (string, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", fmt.Errorf("reload config: %w", err)
	}
	baseDir := filepath.Dir(configPath)
	envPath := filepath.Join(baseDir, ".env")
	if err := Write(cfg, envPath); err != nil {
		return "", err
	}
	return envPath, nil
}
