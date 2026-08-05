package config

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// templateRefsValidator warns on a ${head.path} reference whose head is a
// KNOWN root key (i.e. it actually lives in cfg.Raw — the same set
// CompileVarSyntax accepts, internal/core/project/config's allowedRootKeys)
// but the remaining path does not resolve. That combination is almost always
// a typo: vars: is the one root key with fully free-form content, so
// ${vars.opechatka} where only vars.source.repo was declared renders to a
// literal "${vars.opechatka}" today with no error until the step runs.
//
// Deliberately silent on everything else: an unknown head (shell-style
// ${HOME}, a stray dollar sign) and the special namespaces that never live
// in Raw (param, context, files, host, snapshot, args, generated — see
// Technical Details in the plan) are not this validator's concern. Gating on
// "head present in cfg.Raw" gets both exclusions for free, since Raw's
// top-level keys are exactly allowedRootKeys by the strict-root invariant.
type templateRefsValidator struct{}

var _ validate.Validator = (*templateRefsValidator)(nil)

func (v *templateRefsValidator) ID() string     { return "template_refs" }
func (v *templateRefsValidator) Domain() string { return "config" }

func (v *templateRefsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}

	usages, err := varsusage.EnumerateAllUsages(ctx.ProjectRoot)
	if err != nil {
		// A malformed project is surfaced elsewhere (workspace/parse
		// validators); this validator has nothing useful to add.
		return nil
	}

	var diags []validate.Diagnostic
	for _, u := range usages {
		head, _, _ := strings.Cut(u.Ref, ".")
		if _, known := ctx.Cfg.Raw[head]; !known {
			continue
		}
		if resolvesInRaw(ctx.Cfg.Raw, u.Ref) {
			continue
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "config",
			Target:   "config.template_refs",
			File:     u.File,
			Line:     u.Line,
			Message: fmt.Sprintf(
				"${%s} does not resolve — no such path under %s:",
				u.Ref, head,
			),
			Hint: fmt.Sprintf("check for a typo in %q, or declare the missing path under %s:", u.Ref, head),
		})
	}
	return diags
}

// resolvesInRaw walks a dot-separated path in a nested map, mirroring
// tpl.resolveMapPath's semantics (that helper is unexported, so this is a
// deliberate small duplication rather than exporting a private of a leaf
// package for one caller).
func resolvesInRaw(raw map[string]any, path string) bool {
	if raw == nil {
		return false
	}
	head, rest, hasRest := strings.Cut(path, ".")
	v, ok := raw[head]
	if !ok {
		return false
	}
	if !hasRest {
		return true
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return false
	}
	return resolvesInRaw(sub, rest)
}
