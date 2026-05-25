package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/validate"
)

// knownServiceFiles lists the only filenames allowed in a devbox/services/<name>/ folder.
var knownServiceFiles = map[string]bool{
	"service.yml": true,
	"deploy.yml":  true,
	"reset.yml":   true,
}

type servicesFolderValidator struct{}

func (v *servicesFolderValidator) ID() string {
	return "services-folder"
}

func (v *servicesFolderValidator) Domain() string {
	return "config"
}

func (v *servicesFolderValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic
	servicesDir := filepath.Join(ctx.ProjectRoot, "devbox", "services")

	entries, err := os.ReadDir(servicesDir)
	if err != nil {
		if errors.Is(err, errNotExist) {
			// No services directory — acceptable; servicesValidator handles the absent-dir case.
			return diags
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.services-folder",
			File:     relPath(ctx.ProjectRoot, servicesDir),
			Message:  err.Error(),
		})
		return diags
	}

	var perDiags []validate.Diagnostic
	hasError := false

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		folderPath := filepath.Join(servicesDir, name)

		svcFile := filepath.Join(folderPath, "service.yml")
		if _, err := os.Stat(svcFile); errors.Is(err, os.ErrNotExist) {
			perDiags = append(perDiags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "config",
				Target:   "config.services-folder:" + name,
				File:     relPath(ctx.ProjectRoot, folderPath),
				Message:  fmt.Sprintf("service folder %q has no service.yml", name),
				Hint:     "every service folder must contain a service.yml file",
			})
			hasError = true
		}

		// Scan for unknown files.
		inner, err := os.ReadDir(folderPath)
		if err != nil {
			continue
		}
		for _, fi := range inner {
			if fi.IsDir() {
				continue
			}
			if !knownServiceFiles[fi.Name()] {
				perDiags = append(perDiags, validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Domain:   "config",
					Target:   "config.services-folder:" + name,
					File:     relPath(ctx.ProjectRoot, filepath.Join(folderPath, fi.Name())),
					Message:  fmt.Sprintf("unknown file in service folder %q: %q", name, fi.Name()),
					Hint:     "expected files: service.yml, deploy.yml, reset.yml",
				})
			}
		}
	}

	if !hasError {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   "config",
			Target:   "config.services-folder",
			File:     relPath(ctx.ProjectRoot, servicesDir),
		})
	}
	diags = append(diags, perDiags...)
	return diags
}
