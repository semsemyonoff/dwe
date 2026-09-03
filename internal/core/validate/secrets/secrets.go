// Package secrets validates the project's encrypted-secret setup: the
// committed `secrets.recipient` in workspace.yml, the ENC[age:…] markers in the
// config layers, and the native `.age` sources of config packs.
//
// The domain deliberately ships two validators with different jobs:
//
//   - secrets.recipient is CONTENT. It answers "is this repository internally
//     consistent?" — markers or .age sources without a usable recipient, a
//     damaged marker payload. It needs no private key, so it says the same
//     thing on every machine, and it runs in `dwe validate` only.
//   - secrets.unresolved is READINESS. It answers "can THIS machine read the
//     secrets right now?" and is therefore the second validator (after
//     config.validate) cherry-picked into preflight: a lifecycle command that
//     would render an undecrypted value must stop with a named fix instead of
//     writing ciphertext into a config file.
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	configtmpl "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"
	"github.com/semsemyonoff/dwe/internal/core/execution/templates/packroot"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
)

// domain is the validator domain key (`dwe validate secrets`).
const domain = "secrets"

// packKind is the template-pack kind that may carry .age sources. ide/ai/git
// packs are excluded on purpose: their outputs are git-tracked.
const packKind = "config"

// All returns every secrets validator.
func All() []validate.Validator {
	return []validate.Validator{
		&recipientValidator{},
		&unresolvedValidator{},
	}
}

// UnresolvedValidator returns the readiness validator alone. Preflight and the
// deploy pre-wizard gate cherry-pick it through this constructor rather than
// filtering All() by ID, so adding a content validator to the domain can never
// silently start blocking lifecycle commands.
func UnresolvedValidator() validate.Validator { return &unresolvedValidator{} }

// recipientValidator checks the repository's own consistency; no identity needed.
type recipientValidator struct{}

func (v *recipientValidator) ID() string     { return "recipient" }
func (v *recipientValidator) Domain() string { return domain }

func (v *recipientValidator) Run(ctx validate.Context) []validate.Diagnostic {
	configPath := workspacePath(ctx)
	if configPath == "" {
		return nil
	}
	// Raw layers, not ctx.Cfg: a malformed recipient makes LoadConfig fail, so
	// the scoped `dwe validate secrets` run that is supposed to explain the
	// failure arrives here with a nil Cfg. A read failure is the config
	// domain's diagnostic, not ours.
	layers, err := config.LoadRawLayers(configPath)
	if err != nil {
		return nil
	}

	file := relPath(ctx.ProjectRoot, configPath)
	markers := config.CollectMarkers(layers)
	sources := collectEncryptedSources(ctx)
	recipient := config.RecipientFromLayers(layers)

	var diags []validate.Diagnostic
	emit := func(target, msgFile, msg, hint string) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   domain,
			Target:   target,
			File:     msgFile,
			Message:  msg,
			Hint:     hint,
		})
	}
	if recipient == "" {
		if len(markers) > 0 || len(sources) > 0 {
			emit("secrets.recipient", file,
				fmt.Sprintf("secrets.recipient is not set, but the project carries %s", inventoryPhrase(markers, sources)),
				"run 'dwe secrets init' to mint a project key pair, or restore the secrets.recipient line in workspace.yml")
		}
	} else if err := secrets.ParseRecipient(recipient); err != nil {
		emit("secrets.recipient", file,
			fmt.Sprintf("secrets.recipient %q is not a valid age recipient: %v", recipient, err),
			"the value is the public half printed by 'dwe secrets init' — it starts with age1")
	}

	// A damaged payload is visible without a key: base64 plus the age header.
	// Reporting it here (rather than as "unresolved") stops a keyless developer
	// from hunting for a key that would not have helped.
	for _, m := range markers {
		if err := secrets.CheckMarker(m.Value); err != nil {
			emit("secrets.marker:"+m.Path, relPath(ctx.ProjectRoot, m.Layer),
				fmt.Sprintf("%s: encrypted value is damaged: %v", m.Path, err),
				"restore the value from version control, or re-set it with 'dwe secrets set "+m.Path+"'")
		}
	}

	// A silent domain is indistinguishable from one that did not run:
	// FormatSummary renders an all-zero counter set as "validation skipped (no
	// files found)". Emitting an OK row is what makes `dwe validate secrets`
	// usable as the self-check after onboarding.
	//
	// Only a project that HAS a recipient gets the positive row: without a
	// secrets: block there is nothing to affirm, and `dwe validate` output for
	// projects that use no secrets stays exactly as it was. The message is
	// empty on purpose — the config domain's OK rows read the same way, the
	// table shows the target.
	if len(diags) == 0 && recipient != "" {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityOK,
			Domain:   domain,
			Target:   "secrets.recipient",
			File:     file,
		})
	}

	return diags
}

// unresolvedValidator checks whether this machine can actually read the
// project's secrets right now.
type unresolvedValidator struct{}

func (v *unresolvedValidator) ID() string     { return "unresolved" }
func (v *unresolvedValidator) Domain() string { return domain }

func (v *unresolvedValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		// Readiness is a statement about the loaded config; without one the
		// recipient validator (which raw-loads) carries the diagnosis.
		return nil
	}

	recipient := config.SecretsRecipient(ctx.Cfg)

	// A project that carries neither markers nor a recipient has nothing this
	// validator can say, and it must not pay for the answer: this runs in
	// PREFLIGHT, so the pack scan below (ResolveTemplatePack + LoadManifest per
	// app service) would otherwise hit the disk on every lifecycle command in
	// every project, including the ones with no secrets: block at all. An .age
	// source without a recipient is the recipient validator's content error,
	// which is why skipping the scan here cannot hide one.
	state := ctx.Cfg.SecretsState
	markers := len(state.Decrypted) + len(state.Unresolved)
	if markers == 0 && recipient == "" {
		return nil
	}

	// Past that gate the inventory is built BEFORE any further early return: a
	// project whose only encrypted things are markers must still reach the OK
	// row at the end, and the row's counters need both halves.
	sources := collectEncryptedSources(ctx)
	if markers == 0 && len(sources) == 0 {
		return nil
	}

	diags := unresolvedMarkerDiags(ctx, recipient)

	// With no recipient there is nothing to try: every marker is already
	// reported above and an .age source without a recipient is the recipient
	// validator's content error, not a readiness failure.
	if recipient == "" {
		return diags
	}

	// Where the identity came from, for the OK row. The load pass already
	// answered this when the project carries markers; for an .age-only project
	// nothing has looked yet, so LoadIdentity's own source is kept below.
	source := state.IdentitySource

	if len(sources) > 0 {
		id, idSource, idErr := secrets.LoadIdentity(recipient)
		if idErr != nil {
			files := make([]string, 0, len(sources))
			for _, src := range sources {
				files = append(files, src.rel)
			}
			return append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   domain,
				Target:   "secrets.unresolved:packs",
				Message: fmt.Sprintf("encrypted config-pack source(s) %s cannot be decrypted: %v",
					strings.Join(files, ", "), idErr),
				Hint: identityHint(recipient),
			})
		}
		if source == "" {
			source = string(idSource)
		}

		// Decrypt for real. "The identity loaded" is not the same question as
		// "this file opens": a source encrypted to a previous recipient or
		// truncated in a bad merge fails only here — and it must fail here
		// rather than mid-deploy, after other phases have already run.
		for _, src := range sources {
			data, err := os.ReadFile(src.path)
			if err == nil {
				_, err = secrets.DecryptBytes(data, id)
			}
			if err == nil {
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   domain,
				Target:   "secrets.unresolved:" + src.service + "/" + src.rel,
				File:     relPath(ctx.ProjectRoot, src.path),
				Message: fmt.Sprintf("config pack %q source %s (service %q) cannot be decrypted: %v",
					src.pack, src.rel, src.service, err),
				Hint: "re-encrypt it for " + recipient + " with 'dwe secrets encrypt', or run 'dwe secrets rekey'",
			})
		}
	}

	if len(diags) > 0 {
		return diags
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityOK,
		Domain:   domain,
		Target:   "secrets.unresolved",
		File:     relPath(ctx.ProjectRoot, workspacePath(ctx)),
		Message: fmt.Sprintf("%d encrypted value(s) and %d config-pack source(s) readable via %s",
			markers, len(sources), sourcePhrase(source)),
	}}
}

// sourcePhrase names the identity source for the OK row. It is the source WORD
// (env / env-file / keyfile), never the keyfile path: a positive row must not
// widen what `dwe validate --output json` reveals about the machine.
func sourcePhrase(source string) string {
	if source == "" {
		return "the available identity"
	}
	return source
}

// unresolvedMarkerDiags groups the load-time unresolved markers by reason, one
// diagnostic per reason. A keyless developer has EVERY marker unresolved for
// the same cause; one row per marker would bury the single actionable fix under
// a wall of identical rows in the preflight table.
func unresolvedMarkerDiags(ctx validate.Context, recipient string) []validate.Diagnostic {
	byReason := map[string][]string{}
	for _, u := range ctx.Cfg.SecretsState.Unresolved {
		byReason[u.Reason] = append(byReason[u.Reason], u.Path)
	}
	reasons := make([]string, 0, len(byReason))
	for reason := range byReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)

	var diags []validate.Diagnostic
	for _, reason := range reasons {
		paths := byReason[reason]
		sort.Strings(paths)
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   domain,
			Target:   "secrets.unresolved:" + reason,
			File:     relPath(ctx.ProjectRoot, workspacePath(ctx)),
			Message: fmt.Sprintf("%d encrypted value(s) could not be decrypted (%s): %s",
				len(paths), reasonPhrase(reason, recipient), strings.Join(paths, ", ")),
			Hint: identityHint(recipient),
		})
	}
	return diags
}

// reasonPhrase turns a SecretsState reason into a sentence fragment.
func reasonPhrase(reason, recipient string) string {
	switch reason {
	case config.ReasonNoIdentity:
		return "no identity for " + displayRecipient(recipient) + " is available on this machine"
	case config.ReasonWrongIdentity:
		return "the available identity does not match " + displayRecipient(recipient)
	case config.ReasonCorrupt:
		return "the encrypted payload is damaged"
	default:
		return reason
	}
}

func displayRecipient(recipient string) string {
	if recipient == "" {
		return "the project recipient"
	}
	return recipient
}

// identityHint names every place LoadIdentity looks, in its own precedence
// order, so the fix does not depend on the reader knowing the lookup rules.
func identityHint(recipient string) string {
	location := "~/" + secrets.KeysDirRel + string(os.PathSeparator) + "<recipient>.key"
	if path, err := secrets.KeyfilePath(recipient); err == nil {
		location = path
	}
	return fmt.Sprintf("run 'dwe secrets key import' to store the identity at %s, or set %s / %s",
		location, secrets.EnvKey, secrets.EnvKeyFile)
}

// inventoryPhrase describes what a project holds, for the no-recipient error.
func inventoryPhrase(markers []config.Marker, sources []encSource) string {
	var parts []string
	if len(markers) > 0 {
		parts = append(parts, fmt.Sprintf("%d encrypted value(s) (e.g. %s)", len(markers), markers[0].Path))
	}
	if len(sources) > 0 {
		parts = append(parts, fmt.Sprintf("%d encrypted config-pack source(s) (e.g. %s)", len(sources), sources[0].rel))
	}
	return strings.Join(parts, " and ")
}

// encSource is one .age config-pack source reachable for an enabled app service.
type encSource struct {
	service string
	pack    string
	rel     string // the manifest `from:` value
	path    string // resolved source path (override first, canonical fallback)
}

// collectEncryptedSources lists every .age source the config renderer would
// actually read. It mirrors renderConfigsForRun's iteration —
// config.DeployOrder(cfg, ["app"]) — so a disabled service, or one whose pack
// does not resolve, is invisible here exactly as it is at render time. Any
// resolution failure is skipped: config.generated and the render itself report
// broken packs; a secrets diagnostic about them would be a duplicate.
func collectEncryptedSources(ctx validate.Context) []encSource {
	if ctx.Cfg == nil || ctx.ProjectRoot == "" {
		return nil
	}
	var out []encSource
	for _, name := range config.DeployOrder(ctx.Cfg, []string{"app"}) {
		svc := ctx.Cfg.Services[name]
		_, packName, found, err := configtmpl.ResolveTemplatePack(svc, ctx.Cfg.Services, ctx.ProjectRoot, name)
		if err != nil || !found {
			continue
		}
		m, err := configtmpl.LoadManifest(filepath.Join(ctx.ProjectRoot, "workspace", "templates", packKind, packName))
		if err != nil {
			continue
		}
		for _, entry := range m.Render {
			if !strings.HasSuffix(entry.From, ".age") {
				continue
			}
			path, _, err := packroot.Resolve(ctx.ProjectRoot, packKind, packName, entry.From)
			if err != nil {
				continue
			}
			out = append(out, encSource{service: name, pack: packName, rel: entry.From, path: path})
		}
	}
	return out
}

// workspacePath returns the project's workspace.yml, falling back to the
// conventional location when a scoped run carries no ConfigPath.
func workspacePath(ctx validate.Context) string {
	if ctx.ConfigPath != "" {
		return ctx.ConfigPath
	}
	if ctx.ProjectRoot == "" {
		return ""
	}
	return filepath.Join(ctx.ProjectRoot, "workspace.yml")
}

// relPath renders a path relative to the project root for display.
func relPath(projectRoot, path string) string {
	if projectRoot == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return path
	}
	return rel
}
