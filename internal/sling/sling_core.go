package sling

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/molecule"
	"github.com/gastownhall/gascity/internal/telemetry"
)

// DoSling is the core logic for routing work to an agent.
// Returns a structured result -- no I/O.
func DoSling(opts SlingOpts, deps SlingDeps, querier BeadQuerier) (SlingResult, error) {
	a := opts.Target
	var result SlingResult
	result.Target = a.QualifiedName()

	// Warn about suspended agents / empty pools (unless --force).
	if a.Suspended && !opts.Force {
		result.warn(
			fmt.Sprintf("warning: agent %q is suspended — bead routed but may not be picked up", a.QualifiedName()))
	}
	if deps.ScaleParams != nil && deps.IsMultiSession != nil && deps.IsMultiSession(&a) {
		sp := deps.ScaleParams(&a)
		if sp.Max == 0 && !opts.Force {
			result.warn(
				fmt.Sprintf("warning: pool %q has max=0 — bead routed but no instances to claim it", a.QualifiedName()))
		}
	}

	// Cross-rig guard.
	if !opts.IsFormula && !opts.Force && !opts.DryRun {
		if msg := CheckCrossRig(opts.BeadOrFormula, a, deps.Cfg); msg != "" {
			return result, fmt.Errorf("%s", msg)
		}
	}

	// Pre-flight idempotency check.
	if !opts.IsFormula && !opts.Force {
		check := CheckBeadState(querier, opts.BeadOrFormula, a, deps)
		if check.Idempotent {
			result.Idempotent = true
			result.BeadID = opts.BeadOrFormula
			result.Method = "bead"
			result.msg(
				fmt.Sprintf("Bead %s already routed to %s — skipping (idempotent)", opts.BeadOrFormula, a.QualifiedName()))
			return result, nil
		}
		for _, w := range check.Warnings {
			result.warn(w)
		}
	}

	beadID := opts.BeadOrFormula
	method := "bead"

	if opts.ScopeKind != "" && !opts.IsFormula && opts.OnFormula == "" && (opts.NoFormula || a.EffectiveDefaultSlingFormula() == "") {
		return result, fmt.Errorf("--scope-kind/--scope-ref require a formula-backed workflow launch")
	}

	// If --formula, instantiate wisp and use the root bead ID.
	if opts.IsFormula {
		method = "formula"
		formulaVars := BuildSlingFormulaVars(opts.BeadOrFormula, "", opts.Vars, a, deps)
		mResult, err := InstantiateSlingFormula(context.Background(), opts.BeadOrFormula, SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title: opts.Title,
			Vars:  formulaVars,
		}, "", opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			return result, fmt.Errorf("instantiating formula %q: %w", opts.BeadOrFormula, err)
		}
		if mResult.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, mResult.RootID) {
			wfResult, err := doStartGraphWorkflow(mResult, "", a, method, deps)
			if err != nil {
				return result, err
			}
			wfResult.msg(
				fmt.Sprintf("Started workflow %s (formula %q) → %s", mResult.RootID, opts.BeadOrFormula, a.QualifiedName()))
			return wfResult, nil
		}
		beadID = mResult.RootID
	}

	// If --on, attach a wisp to the bead and route the original bead.
	if opts.OnFormula != "" {
		method = "on-formula"
		if err := CheckNoMoleculeChildren(querier, beadID, deps.Store, &result); err != nil {
			return result, fmt.Errorf("%w", err)
		}
		formulaVars := BuildSlingFormulaVars(opts.OnFormula, beadID, opts.Vars, a, deps)
		mResult, err := InstantiateSlingFormula(context.Background(), opts.OnFormula, SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title:            opts.Title,
			Vars:             formulaVars,
			PriorityOverride: BeadPriorityOverride(querier, beadID),
		}, beadID, opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			return result, fmt.Errorf("instantiating formula %q on %s: %w", opts.OnFormula, beadID, err)
		}
		wispRootID := mResult.RootID
		if mResult.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, wispRootID) {
			wfResult, err := doStartGraphWorkflow(mResult, beadID, a, method, deps)
			if err != nil {
				return result, err
			}
			wfResult.msg(
				fmt.Sprintf("Attached workflow %s (formula %q) to %s", wispRootID, opts.OnFormula, beadID))
			return wfResult, nil
		}
		if err := deps.Store.SetMetadata(beadID, "molecule_id", wispRootID); err != nil {
			result.warn(
				fmt.Sprintf("setting molecule_id on %s: %v", beadID, err))
		}
		result.msg(
			fmt.Sprintf("Attached wisp %s (formula %q) to %s", wispRootID, opts.OnFormula, beadID))
	}

	// Apply default formula if target has one and no explicit formula/--no-formula.
	if opts.OnFormula == "" && !opts.IsFormula && !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		method = "default-on-formula"
		if err := CheckNoMoleculeChildren(querier, beadID, deps.Store, &result); err != nil {
			return result, fmt.Errorf("%w", err)
		}
		defaultVars := BuildSlingFormulaVars(a.EffectiveDefaultSlingFormula(), beadID, opts.Vars, a, deps)
		mResult, err := InstantiateSlingFormula(context.Background(), a.EffectiveDefaultSlingFormula(), SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title:            opts.Title,
			Vars:             defaultVars,
			PriorityOverride: BeadPriorityOverride(querier, beadID),
		}, beadID, opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			return result, fmt.Errorf("instantiating default formula %q on %s: %w",
				a.EffectiveDefaultSlingFormula(), beadID, err)
		}
		wispRootID := mResult.RootID
		if mResult.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, wispRootID) {
			wfResult, err := doStartGraphWorkflow(mResult, beadID, a, method, deps)
			if err != nil {
				return result, err
			}
			wfResult.msg(
				fmt.Sprintf("Attached workflow %s (default formula %q) to %s", wispRootID, a.EffectiveDefaultSlingFormula(), beadID))
			return wfResult, nil
		}
		if err := deps.Store.SetMetadata(beadID, "molecule_id", wispRootID); err != nil {
			result.warn(
				fmt.Sprintf("setting molecule_id on %s: %v", beadID, err))
		}
		result.msg(
			fmt.Sprintf("Attached wisp %s (default formula %q) to %s",
				wispRootID, a.EffectiveDefaultSlingFormula(), beadID))
	}

	// Build and execute sling command.
	slingEnv := ResolveSlingEnv(a, deps)
	slingCmd := BuildSlingCommand(a.EffectiveSlingQuery(), beadID)
	rigDir := SlingDirForBead(deps.Cfg, deps.CityPath, beadID)
	if _, err := deps.Runner(rigDir, slingCmd, slingEnv); err != nil {
		telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), method, err)
		return result, fmt.Errorf("%w", err)
	}

	telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), method, nil)

	// Merge strategy metadata.
	if opts.Merge != "" && deps.Store != nil {
		if err := deps.Store.SetMetadata(beadID, "merge_strategy", opts.Merge); err != nil {
			result.warn(
				fmt.Sprintf("setting merge strategy: %v", err))
		}
	}

	// Auto-convoy.
	if !opts.NoConvoy && !opts.IsFormula && deps.Store != nil {
		var convoyLabels []string
		if opts.Owned {
			convoyLabels = []string{"owned"}
		}
		convoy, err := deps.Store.Create(beads.Bead{
			Title:  fmt.Sprintf("sling-%s", beadID),
			Type:   "convoy",
			Labels: convoyLabels,
		})
		if err != nil {
			result.warn(
				fmt.Sprintf("creating auto-convoy: %v", err))
		} else {
			parentID := convoy.ID
			if err := deps.Store.Update(beadID, beads.UpdateOpts{ParentID: &parentID}); err != nil {
				result.warn(
					fmt.Sprintf("linking bead to convoy: %v", err))
			} else {
				result.ConvoyID = convoy.ID
				label := ""
				if opts.Owned {
					label = " (owned)"
				}
				result.msg(
					fmt.Sprintf("Auto-convoy %s%s", convoy.ID, label))
			}
		}
	}

	// Final message.
	switch {
	case opts.IsFormula:
		result.msg(
			fmt.Sprintf("Slung formula %q (wisp root %s) → %s", opts.BeadOrFormula, beadID, a.QualifiedName()))
	case opts.OnFormula != "":
		result.msg(
			fmt.Sprintf("Slung %s (with formula %q) → %s", beadID, opts.OnFormula, a.QualifiedName()))
	default:
		result.msg(
			fmt.Sprintf("Slung %s → %s", beadID, a.QualifiedName()))
	}

	result.BeadID = beadID
	result.Method = method

	// Poke controller for immediate reconciliation.
	if !opts.SkipPoke && deps.PokeController != nil {
		_ = deps.PokeController(deps.CityPath)
	}

	// Signal that nudge is needed (caller handles actual nudge).
	if opts.Nudge {
		result.NudgeAgent = &a
	}

	return result, nil
}

// doStartGraphWorkflow performs post-instantiation graph workflow setup.
func doStartGraphWorkflow(mResult *molecule.Result, sourceBeadID string, a config.Agent, method string, deps SlingDeps) (SlingResult, error) {
	var result SlingResult
	result.Target = a.QualifiedName()
	result.Method = method
	result.WorkflowID = mResult.RootID
	result.BeadID = mResult.RootID

	rootID := mResult.RootID
	SlingTracef("workflow-start begin root=%s source=%s agent=%s method=%s", rootID, sourceBeadID, a.QualifiedName(), method)

	if err := PromoteWorkflowLaunchBead(deps.Store, rootID); err != nil {
		return result, fmt.Errorf("setting workflow root %s in_progress: %w", rootID, err)
	}
	if sourceBeadID != "" {
		if err := deps.Store.SetMetadata(sourceBeadID, "workflow_id", rootID); err != nil {
			return result, fmt.Errorf("setting workflow_id on %s: %w", sourceBeadID, err)
		}
	}
	telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), method, nil)
	if deps.PokeController != nil {
		_ = deps.PokeController(deps.CityPath)
	}
	if deps.PokeControlDispatch != nil {
		_ = deps.PokeControlDispatch(deps.CityPath)
	}
	return result, nil
}

// DoSlingBatch handles convoy expansion before delegating to DoSling.
func DoSlingBatch(opts SlingOpts, deps SlingDeps, querier BeadChildQuerier) (SlingResult, error) {
	a := opts.Target

	// Formula mode, nil querier → delegate directly.
	if opts.IsFormula || querier == nil {
		return DoSling(opts, deps, querier)
	}

	b, err := querier.Get(opts.BeadOrFormula)
	if err != nil {
		singleOpts := opts
		singleOpts.IsFormula = false
		return DoSling(singleOpts, deps, querier)
	}
	if b.Type == "epic" {
		return SlingResult{}, fmt.Errorf("bead %s is an epic; first-class support is for convoys only", b.ID)
	}

	if !beads.IsContainerType(b.Type) {
		singleOpts := opts
		singleOpts.IsFormula = false
		return DoSling(singleOpts, deps, querier)
	}

	children, err := querier.List(beads.ListQuery{
		ParentID:      b.ID,
		IncludeClosed: true,
		Sort:          beads.SortCreatedAsc,
	})
	if err != nil {
		return SlingResult{}, fmt.Errorf("listing children of %s: %w", b.ID, err)
	}

	var open, skipped []beads.Bead
	for _, c := range children {
		if c.Status == "open" {
			open = append(open, c)
		} else {
			skipped = append(skipped, c)
		}
	}

	if len(open) == 0 {
		return SlingResult{}, fmt.Errorf("%s %s has no open children", b.Type, b.ID)
	}

	// Cross-rig guard on container.
	if !opts.Force && !opts.DryRun {
		if msg := CheckCrossRig(b.ID, a, deps.Cfg); msg != "" {
			return SlingResult{}, fmt.Errorf("%s", msg)
		}
	}

	// Pre-check molecule attachments.
	var batchResult SlingResult
	batchResult.Target = a.QualifiedName()
	useFormula := opts.OnFormula
	if useFormula == "" && !opts.IsFormula && !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		useFormula = a.EffectiveDefaultSlingFormula()
	}
	if useFormula != "" {
		if err := CheckBatchNoMoleculeChildren(querier, open, deps.Store, &batchResult); err != nil {
			return batchResult, fmt.Errorf("%w", err)
		}
	}

	batchResult.msg(
		fmt.Sprintf("Expanding %s %s (%d children, %d open)", b.Type, b.ID, len(children), len(open)))

	batchMethod := "batch"
	if opts.OnFormula != "" {
		batchMethod = "batch-on"
	} else if !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		batchMethod = "batch-default-on"
	}
	batchResult.Method = batchMethod
	batchResult.Total = len(children)

	routed := 0
	failed := 0
	idempotent := 0
	for _, child := range open {
		if !opts.Force {
			check := CheckBeadState(querier, child.ID, a, deps)
			if check.Idempotent {
				batchResult.msg(
					fmt.Sprintf("  Skipped %s — already routed to %s", child.ID, a.QualifiedName()))
				idempotent++
				continue
			}
			for _, w := range check.Warnings {
				batchResult.warn(w)
			}
		}

		// Attach wisp if --on.
		if opts.OnFormula != "" {
			childVars := BuildSlingFormulaVars(opts.OnFormula, child.ID, opts.Vars, a, deps)
			cookResult, err := molecule.Cook(context.Background(), deps.Store, opts.OnFormula, SlingFormulaSearchPaths(deps, a), molecule.Options{
				Title:            opts.Title,
				Vars:             childVars,
				PriorityOverride: ClonePriorityPtr(child.Priority),
			})
			if err != nil {
				batchResult.warn(
					fmt.Sprintf("  Failed %s: instantiating formula %q: %v", child.ID, opts.OnFormula, err))
				telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
				failed++
				continue
			}
			_ = deps.Store.SetMetadata(child.ID, "molecule_id", cookResult.RootID)
			batchResult.msg(
				fmt.Sprintf("  Attached wisp %s → %s", cookResult.RootID, child.ID))
		} else if !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
			childVars := BuildSlingFormulaVars(a.EffectiveDefaultSlingFormula(), child.ID, opts.Vars, a, deps)
			cookResult, err := molecule.Cook(context.Background(), deps.Store, a.EffectiveDefaultSlingFormula(), SlingFormulaSearchPaths(deps, a), molecule.Options{
				Title:            opts.Title,
				Vars:             childVars,
				PriorityOverride: ClonePriorityPtr(child.Priority),
			})
			if err != nil {
				batchResult.warn(
					fmt.Sprintf("  Failed %s: instantiating default formula %q: %v", child.ID, a.EffectiveDefaultSlingFormula(), err))
				telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
				failed++
				continue
			}
			_ = deps.Store.SetMetadata(child.ID, "molecule_id", cookResult.RootID)
			batchResult.msg(
				fmt.Sprintf("  Attached wisp %s (default formula) → %s", cookResult.RootID, child.ID))
		}

		childEnv := ResolveSlingEnv(a, deps)
		slingCmd := BuildSlingCommand(a.EffectiveSlingQuery(), child.ID)
		rigDir := SlingDirForBead(deps.Cfg, deps.CityPath, child.ID)
		if _, err := deps.Runner(rigDir, slingCmd, childEnv); err != nil {
			batchResult.warn(
				fmt.Sprintf("  Failed %s: %v", child.ID, err))
			telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
			failed++
			continue
		}

		telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, nil)
		batchResult.msg(
			fmt.Sprintf("  Slung %s → %s", child.ID, a.QualifiedName()))
		routed++
	}

	for _, child := range skipped {
		batchResult.msg(
			fmt.Sprintf("  Skipped %s (status: %s)", child.ID, child.Status))
	}

	summary := fmt.Sprintf("Slung %d/%d children of %s → %s", routed, len(children), b.ID, a.QualifiedName())
	if idempotent > 0 {
		summary += fmt.Sprintf(" (%d already routed)", idempotent)
	}
	batchResult.msg( summary)
	batchResult.Routed = routed
	batchResult.Failed = failed
	batchResult.Skipped = idempotent + len(skipped)

	if opts.Nudge && routed > 0 {
		batchResult.NudgeAgent = &a
	}

	if failed > 0 {
		return batchResult, fmt.Errorf("%d/%d children failed", failed, len(open))
	}
	return batchResult, nil
}
