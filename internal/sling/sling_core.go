package sling

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/molecule"
	"github.com/gastownhall/gascity/internal/telemetry"
)

// DoSling is the core logic for routing work to an agent.
func DoSling(opts SlingOpts, deps SlingDeps, querier BeadQuerier) int {
	a := opts.Target
	// Warn about suspended agents / empty pools (unless --force).
	if a.Suspended && !opts.Force {
		fmt.Fprintf(deps.Stderr, "warning: agent %q is suspended — bead routed but may not be picked up\n", a.QualifiedName()) //nolint:errcheck // best-effort
	}
	if deps.ScaleParams != nil && deps.IsMultiSession != nil && deps.IsMultiSession(&a) {
		sp := deps.ScaleParams(&a)
		if sp.Max == 0 && !opts.Force {
			fmt.Fprintf(deps.Stderr, "warning: pool %q has max=0 — bead routed but no instances to claim it\n", a.QualifiedName()) //nolint:errcheck // best-effort
		}
	}

	// Cross-rig guard.
	if !opts.IsFormula && !opts.Force && !opts.DryRun {
		if msg := CheckCrossRig(opts.BeadOrFormula, a, deps.Cfg); msg != "" {
			fmt.Fprintln(deps.Stderr, msg) //nolint:errcheck // best-effort
			return 1
		}
	}

	// Pre-flight idempotency check.
	if !opts.IsFormula && !opts.Force {
		result := CheckBeadState(querier, opts.BeadOrFormula, a, deps)
		if result.Idempotent {
			if opts.DryRun && deps.DryRunSingle != nil {
				return deps.DryRunSingle(opts, deps, querier)
			}
			fmt.Fprintf(deps.Stdout, "Bead %s already routed to %s — skipping (idempotent)\n", opts.BeadOrFormula, a.QualifiedName()) //nolint:errcheck // best-effort
			return 0
		}
		for _, w := range result.Warnings {
			fmt.Fprintln(deps.Stderr, w) //nolint:errcheck // best-effort
		}
	}

	// Dry-run: resolve and print preview without executing.
	if opts.DryRun && deps.DryRunSingle != nil {
		return deps.DryRunSingle(opts, deps, querier)
	}

	beadID := opts.BeadOrFormula
	method := "bead"

	if opts.ScopeKind != "" && !opts.IsFormula && opts.OnFormula == "" && (opts.NoFormula || a.EffectiveDefaultSlingFormula() == "") {
		fmt.Fprintln(deps.Stderr, "gc sling: --scope-kind/--scope-ref require a formula-backed workflow launch") //nolint:errcheck // best-effort
		return 1
	}

	// If --formula, instantiate wisp and use the root bead ID.
	if opts.IsFormula {
		method = "formula"
		formulaVars := BuildSlingFormulaVars(opts.BeadOrFormula, "", opts.Vars, a, deps)
		result, err := InstantiateSlingFormula(context.Background(), opts.BeadOrFormula, SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title: opts.Title,
			Vars:  formulaVars,
		}, "", opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: instantiating formula %q: %v\n", opts.BeadOrFormula, err) //nolint:errcheck // best-effort
			return 1
		}
		if result.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, result.RootID) {
			if code := StartGraphWorkflow(result, "", a, method, deps); code != 0 {
				return code
			}
			fmt.Fprintf(deps.Stdout, "Started workflow %s (formula %q) → %s\n", result.RootID, opts.BeadOrFormula, a.QualifiedName()) //nolint:errcheck // best-effort
			return 0
		}
		beadID = result.RootID
	}

	// If --on, attach a wisp to the bead and route the original bead.
	if opts.OnFormula != "" {
		method = "on-formula"
		if err := CheckNoMoleculeChildren(querier, beadID, deps.Store, deps.Stderr); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: %v\n", err) //nolint:errcheck // best-effort
			return 1
		}
		formulaVars := BuildSlingFormulaVars(opts.OnFormula, beadID, opts.Vars, a, deps)
		result, err := InstantiateSlingFormula(context.Background(), opts.OnFormula, SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title:            opts.Title,
			Vars:             formulaVars,
			PriorityOverride: BeadPriorityOverride(querier, beadID),
		}, beadID, opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: instantiating formula %q on %s: %v\n", opts.OnFormula, beadID, err) //nolint:errcheck // best-effort
			return 1
		}
		wispRootID := result.RootID
		if result.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, wispRootID) {
			if code := StartGraphWorkflow(result, beadID, a, method, deps); code != 0 {
				return code
			}
			fmt.Fprintf(deps.Stdout, "Attached workflow %s (formula %q) to %s\n", wispRootID, opts.OnFormula, beadID) //nolint:errcheck // best-effort
			return 0
		}
		if err := deps.Store.SetMetadata(beadID, "molecule_id", wispRootID); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: setting molecule_id on %s: %v\n", beadID, err) //nolint:errcheck // best-effort
		}
		fmt.Fprintf(deps.Stdout, "Attached wisp %s (formula %q) to %s\n", wispRootID, opts.OnFormula, beadID) //nolint:errcheck // best-effort
	}

	// Apply default formula if target has one and no explicit formula/--no-formula.
	if opts.OnFormula == "" && !opts.IsFormula && !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		method = "default-on-formula"
		if err := CheckNoMoleculeChildren(querier, beadID, deps.Store, deps.Stderr); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: %v\n", err) //nolint:errcheck // best-effort
			return 1
		}
		defaultVars := BuildSlingFormulaVars(a.EffectiveDefaultSlingFormula(), beadID, opts.Vars, a, deps)
		result, err := InstantiateSlingFormula(context.Background(), a.EffectiveDefaultSlingFormula(), SlingFormulaSearchPaths(deps, a), molecule.Options{
			Title:            opts.Title,
			Vars:             defaultVars,
			PriorityOverride: BeadPriorityOverride(querier, beadID),
		}, beadID, opts.ScopeKind, opts.ScopeRef, a, deps)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: instantiating default formula %q on %s: %v\n",
				a.EffectiveDefaultSlingFormula(), beadID, err) //nolint:errcheck // best-effort
			return 1
		}
		wispRootID := result.RootID
		if result.GraphWorkflow || IsGraphWorkflowAttachment(deps.Store, wispRootID) {
			if code := StartGraphWorkflow(result, beadID, a, method, deps); code != 0 {
				return code
			}
			fmt.Fprintf(deps.Stdout, "Attached workflow %s (default formula %q) to %s\n", wispRootID, a.EffectiveDefaultSlingFormula(), beadID) //nolint:errcheck // best-effort
			return 0
		}
		if err := deps.Store.SetMetadata(beadID, "molecule_id", wispRootID); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: setting molecule_id on %s: %v\n", beadID, err) //nolint:errcheck // best-effort
		}
		fmt.Fprintf(deps.Stdout, "Attached wisp %s (default formula %q) to %s\n",
			wispRootID, a.EffectiveDefaultSlingFormula(), beadID) //nolint:errcheck // best-effort
	}

	// Build and execute sling command.
	slingEnv := ResolveSlingEnv(a, deps)
	slingCmd := BuildSlingCommand(a.EffectiveSlingQuery(), beadID)
	rigDir := SlingDirForBead(deps.Cfg, deps.CityPath, beadID)
	if _, err := deps.Runner(rigDir, slingCmd, slingEnv); err != nil {
		fmt.Fprintf(deps.Stderr, "gc sling: %v\n", err) //nolint:errcheck // best-effort
		telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), method, err)
		return 1
	}

	telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), method, nil)

	// Merge strategy metadata.
	if opts.Merge != "" && deps.Store != nil {
		if err := deps.Store.SetMetadata(beadID, "merge_strategy", opts.Merge); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: setting merge strategy: %v\n", err) //nolint:errcheck // best-effort
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
			fmt.Fprintf(deps.Stderr, "gc sling: creating auto-convoy: %v\n", err) //nolint:errcheck // best-effort
		} else {
			parentID := convoy.ID
			if err := deps.Store.Update(beadID, beads.UpdateOpts{ParentID: &parentID}); err != nil {
				fmt.Fprintf(deps.Stderr, "gc sling: linking bead to convoy: %v\n", err) //nolint:errcheck // best-effort
			} else {
				label := ""
				if opts.Owned {
					label = " (owned)"
				}
				fmt.Fprintf(deps.Stdout, "Auto-convoy %s%s\n", convoy.ID, label) //nolint:errcheck // best-effort
			}
		}
	}

	switch {
	case opts.IsFormula:
		fmt.Fprintf(deps.Stdout, "Slung formula %q (wisp root %s) → %s\n", opts.BeadOrFormula, beadID, a.QualifiedName()) //nolint:errcheck // best-effort
	case opts.OnFormula != "":
		fmt.Fprintf(deps.Stdout, "Slung %s (with formula %q) → %s\n", beadID, opts.OnFormula, a.QualifiedName()) //nolint:errcheck // best-effort
	default:
		fmt.Fprintf(deps.Stdout, "Slung %s → %s\n", beadID, a.QualifiedName()) //nolint:errcheck // best-effort
	}

	// Poke controller for immediate reconciliation.
	if !opts.SkipPoke && deps.PokeController != nil {
		_ = deps.PokeController(deps.CityPath)
	}

	// Nudge target if requested.
	if opts.Nudge && deps.DoNudge != nil {
		deps.DoNudge(&a, deps.CityName, deps.CityPath, deps.Cfg, deps.SP, deps.Store, deps.Stdout, deps.Stderr)
	}

	return 0
}

// DoSlingBatch handles convoy expansion before delegating to DoSling.
func DoSlingBatch(opts SlingOpts, deps SlingDeps, querier BeadChildQuerier) int {
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
		fmt.Fprintf(deps.Stderr, "gc sling: bead %s is an epic; first-class support is for convoys only\n", b.ID) //nolint:errcheck // best-effort
		return 1
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
		fmt.Fprintf(deps.Stderr, "gc sling: listing children of %s: %v\n", b.ID, err) //nolint:errcheck // best-effort
		return 1
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
		fmt.Fprintf(deps.Stderr, "gc sling: %s %s has no open children\n", b.Type, b.ID) //nolint:errcheck // best-effort
		return 1
	}

	// Cross-rig guard on container.
	if !opts.Force && !opts.DryRun {
		if msg := CheckCrossRig(b.ID, a, deps.Cfg); msg != "" {
			fmt.Fprintln(deps.Stderr, msg) //nolint:errcheck // best-effort
			return 1
		}
	}

	// Pre-check molecule attachments.
	useFormula := opts.OnFormula
	if useFormula == "" && !opts.IsFormula && !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		useFormula = a.EffectiveDefaultSlingFormula()
	}
	if useFormula != "" {
		if err := CheckBatchNoMoleculeChildren(querier, open, deps.Store, deps.Stderr); err != nil {
			fmt.Fprintf(deps.Stderr, "gc sling: %v\n", err) //nolint:errcheck // best-effort
			return 1
		}
	}

	// Dry-run.
	if opts.DryRun && deps.DryRunBatch != nil {
		return deps.DryRunBatch(opts, deps, querier)
	}

	fmt.Fprintf(deps.Stdout, "Expanding %s %s (%d children, %d open)\n", b.Type, b.ID, len(children), len(open)) //nolint:errcheck // best-effort

	batchMethod := "batch"
	if opts.OnFormula != "" {
		batchMethod = "batch-on"
	} else if !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
		batchMethod = "batch-default-on"
	}

	routed := 0
	failed := 0
	idempotent := 0
	for _, child := range open {
		if !opts.Force {
			result := CheckBeadState(querier, child.ID, a, deps)
			if result.Idempotent {
				fmt.Fprintf(deps.Stdout, "  Skipped %s — already routed to %s\n", child.ID, a.QualifiedName()) //nolint:errcheck // best-effort
				idempotent++
				continue
			}
			for _, w := range result.Warnings {
				fmt.Fprintln(deps.Stderr, w) //nolint:errcheck // best-effort
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
				fmt.Fprintf(deps.Stderr, "  Failed %s: instantiating formula %q: %v\n", child.ID, opts.OnFormula, err) //nolint:errcheck // best-effort
				telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
				failed++
				continue
			}
			_ = deps.Store.SetMetadata(child.ID, "molecule_id", cookResult.RootID)
			fmt.Fprintf(deps.Stdout, "  Attached wisp %s → %s\n", cookResult.RootID, child.ID) //nolint:errcheck // best-effort
		} else if !opts.NoFormula && a.EffectiveDefaultSlingFormula() != "" {
			childVars := BuildSlingFormulaVars(a.EffectiveDefaultSlingFormula(), child.ID, opts.Vars, a, deps)
			cookResult, err := molecule.Cook(context.Background(), deps.Store, a.EffectiveDefaultSlingFormula(), SlingFormulaSearchPaths(deps, a), molecule.Options{
				Title:            opts.Title,
				Vars:             childVars,
				PriorityOverride: ClonePriorityPtr(child.Priority),
			})
			if err != nil {
				fmt.Fprintf(deps.Stderr, "  Failed %s: instantiating default formula %q: %v\n", child.ID, a.EffectiveDefaultSlingFormula(), err) //nolint:errcheck // best-effort
				telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
				failed++
				continue
			}
			_ = deps.Store.SetMetadata(child.ID, "molecule_id", cookResult.RootID)
			fmt.Fprintf(deps.Stdout, "  Attached wisp %s (default formula) → %s\n", cookResult.RootID, child.ID) //nolint:errcheck // best-effort
		}

		childEnv := ResolveSlingEnv(a, deps)
		slingCmd := BuildSlingCommand(a.EffectiveSlingQuery(), child.ID)
		rigDir := SlingDirForBead(deps.Cfg, deps.CityPath, child.ID)
		if _, err := deps.Runner(rigDir, slingCmd, childEnv); err != nil {
			fmt.Fprintf(deps.Stderr, "  Failed %s: %v\n", child.ID, err) //nolint:errcheck // best-effort
			telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, err)
			failed++
			continue
		}

		telemetry.RecordSling(context.Background(), a.QualifiedName(), TargetType(&a), batchMethod, nil)
		fmt.Fprintf(deps.Stdout, "  Slung %s → %s\n", child.ID, a.QualifiedName()) //nolint:errcheck // best-effort
		routed++
	}

	for _, child := range skipped {
		fmt.Fprintf(deps.Stdout, "  Skipped %s (status: %s)\n", child.ID, child.Status) //nolint:errcheck // best-effort
	}

	summary := fmt.Sprintf("Slung %d/%d children of %s → %s", routed, len(children), b.ID, a.QualifiedName())
	if idempotent > 0 {
		summary += fmt.Sprintf(" (%d already routed)", idempotent)
	}
	fmt.Fprintln(deps.Stdout, summary) //nolint:errcheck // best-effort

	if opts.Nudge && routed > 0 && deps.DoNudge != nil {
		deps.DoNudge(&a, deps.CityName, deps.CityPath, deps.Cfg, deps.SP, deps.Store, deps.Stdout, deps.Stderr)
	}

	if failed > 0 {
		return 1
	}
	return 0
}
