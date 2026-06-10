package registry

import (
	"log/slog"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// evalHideFn is the package-level seam used to evaluate hide expressions.
// Tests swap it to avoid spinning up template/shell machinery. Production
// code always uses tpl.EvalCommandCondition.
var evalHideFn = tpl.EvalCommandCondition

// ApplyVisibility evaluates the Hide expression on every group and command
// in the registry against cfg, populating GroupNode.Hidden and
// CommandDef.Hidden. Group hides cascade: any descendant of a hidden group
// is forced Hidden regardless of its own Hide expression.
//
// Empty Hide expressions are treated as "not hidden" (the opposite of how
// EvalCommandCondition treats an empty `when:` — there empty is truthy/run,
// here empty means visible).
//
// Fail-open: a per-expression evaluation failure (template execute error,
// predicate failure, missing nested map keys) does NOT abort the pass and
// does NOT return an error. The failing entry is treated as visible
// (Hidden=false), and the failure is logged via slog.Warn so an operator
// can diagnose. The validator catches static syntax issues at design time;
// runtime evaluation failures must not brick the CLI.
//
// Idempotent: calling twice with the same cfg yields the same result.
// The error return is reserved for unrecoverable structural problems
// (currently none — kept for forward compatibility); callers may safely
// ignore it.
//
// Must be called once after LoadRegistry and before any caller queries
// (List, Get, browse, etc.). projectRoot is forwarded to predicate
// evaluation (cmd:/builtin:) used inside Hide expressions.
func (r *Registry) ApplyVisibility(cfg *config.DweConfig, projectRoot string) error {
	if r == nil {
		return nil
	}
	rctx := &tpl.RenderContext{
		Host: tpl.CurrentHostInfo(),
	}
	if cfg != nil {
		rctx.Raw = cfg.Raw
	}

	// Invariant: applyGroupVisibility MUST complete fully before the
	// byID loop below. The per-command cascade reads parent.Hidden via
	// r.groups[cmd.Group], which is only correct after the DFS has
	// populated every group node. A future refactor combining the two
	// passes into a single walk would silently break cascade for
	// descendants visited before their parent in map-iteration order.
	r.applyGroupVisibility(r.root, false, rctx, projectRoot)

	// Container-surface visibility is a separate axis from Hide: it gates
	// listing/completion/inspect/direct invocation but NOT executability
	// (workflow steps keep running non-bridged sub-commands host-side), so
	// it gets its own resolved field instead of folding into Hidden.
	r.applyBridgeVisibility()

	for _, cmd := range r.byID {
		parent, ok := r.groups[cmd.Group]
		if ok && parent.Hidden {
			cmd.Hidden = true
			continue
		}
		// Optimisation: when cmd.Hide is the same expression as the
		// owning group's Meta.Hide (the typical case for daemon
		// synthetics that inherit src.Hide from expansion), inherit
		// parent.Hidden directly. The group's DFS already evaluated
		// this same string; re-evaluating per-synthetic would multiply
		// shell-predicate cost by the synthetic count.
		if ok && cmd.Hide != "" && cmd.Hide == parent.Meta.Hide {
			cmd.Hidden = parent.Hidden
			continue
		}
		hidden, err := evalHide(cmd.Hide, rctx, projectRoot)
		if err != nil {
			// Fail-open: leave cmd.Hidden untouched so a transient
			// per-expression failure cannot un-hide a previously
			// hidden command on a subsequent re-apply.
			slog.Warn("hide evaluation failed; leaving command visibility unchanged",
				"id", cmd.ID, "expr", cmd.Hide, "err", err)
			continue
		}
		cmd.Hidden = hidden
	}

	return nil
}

// applyGroupVisibility walks the group tree depth-first. A node is Hidden
// when its parent is Hidden (cascade) OR its own Meta.Hide evaluates true.
// The cascade flag short-circuits child evaluation so the explicit
// "cascade wins over child override" semantics is preserved.
//
// Per-group eval failures are logged and treated as visible (fail-open).
func (r *Registry) applyGroupVisibility(node *GroupNode, parentHidden bool, rctx *tpl.RenderContext, projectRoot string) {
	if node == nil {
		return
	}
	if parentHidden {
		node.Hidden = true
	} else {
		hidden, err := evalHide(node.Meta.Hide, rctx, projectRoot)
		if err != nil {
			// Fail-open: leave node.Hidden untouched so a transient eval
			// failure cannot un-hide a group that was previously hidden.
			slog.Warn("hide evaluation failed; leaving group visibility unchanged",
				"group", node.ID, "expr", node.Meta.Hide, "err", err)
		} else {
			node.Hidden = hidden
		}
	}
	for _, child := range node.Children {
		r.applyGroupVisibility(child, node.Hidden, rctx, projectRoot)
	}
}

// applyBridgeVisibility resolves CommandDef.BridgeHidden for every command:
// outside a bridged invocation everything is visible (explicitly reset so
// the pass stays idempotent); inside one, a command is visible only when the
// field-wise merge of its own `bridge:` block over the nearest ancestor
// group's effective block admits the calling service (opt-in default — no
// block anywhere means host-only). Pure data, no expressions: unlike Hide
// there is no failure mode and no fail-open path to worry about.
func (r *Registry) applyBridgeVisibility() {
	if !bridgeclient.InContainer() {
		for _, cmd := range r.byID {
			cmd.BridgeHidden = false
		}
		return
	}
	caller := bridgeclient.CallingService()
	eff := make(map[string]*model.BridgeDef, len(r.groups))
	collectEffectiveBridge(r.root, nil, eff)
	for _, cmd := range r.byID {
		cmd.BridgeHidden = !model.MergeBridge(eff[cmd.Group], cmd.Bridge).AllowedFrom(caller)
	}
}

// collectEffectiveBridge walks the group tree depth-first, merging each
// node's `group.bridge` block over its parent's effective block (deeper
// files override shallower ones field-wise) into eff keyed by group ID.
func collectEffectiveBridge(node *GroupNode, parent *model.BridgeDef, eff map[string]*model.BridgeDef) {
	if node == nil {
		return
	}
	cur := model.MergeBridge(parent, node.Meta.Bridge)
	eff[node.ID] = cur
	for _, child := range node.Children {
		collectEffectiveBridge(child, cur, eff)
	}
}

// evalHide treats an empty expression as "not hidden". Otherwise it
// delegates to evalHideFn (tpl.EvalCommandCondition by default), whose
// truthy result means "hide".
func evalHide(expr string, rctx *tpl.RenderContext, projectRoot string) (bool, error) {
	if expr == "" {
		return false, nil
	}
	return evalHideFn(expr, rctx, projectRoot)
}
