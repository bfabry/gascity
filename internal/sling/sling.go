// Package sling implements work routing operations for Gas City.
// It provides DoSling and DoSlingBatch for dispatching beads to agents,
// including formula instantiation, graph workflow decoration, and
// convoy auto-creation.
package sling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/molecule"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/shellquote"
)

// BeadQuerier can retrieve a single bead by ID.
type BeadQuerier interface {
	Get(id string) (beads.Bead, error)
}

// BeadChildQuerier extends BeadQuerier with the ability to query child beads.
type BeadChildQuerier interface {
	BeadQuerier
	List(query beads.ListQuery) ([]beads.Bead, error)
}

// SlingOpts captures the user's intent for a sling operation.
type SlingOpts struct {
	Target        config.Agent
	BeadOrFormula string
	IsFormula     bool
	OnFormula     string
	NoFormula     bool
	SkipPoke      bool
	Title         string
	Vars          []string
	Merge         string // "", "direct", "mr", "local"
	NoConvoy      bool
	Owned         bool
	Nudge         bool
	Force         bool
	DryRun        bool
	ScopeKind     string
	ScopeRef      string
}

// SlingDeps bundles infrastructure dependencies injected for testability.
// No I/O fields -- domain functions return structured results.
type SlingDeps struct {
	CityName string
	CityPath string
	Cfg      *config.City
	SP       runtime.Provider
	Runner   SlingRunner
	Store    beads.Store
	StoreRef string

	// Injected functions from cmd/gc that sling cannot import directly.
	ResolveAgent        func(cfg *config.City, name, rigContext string) (config.Agent, bool)
	IsMultiSession      func(a *config.Agent) bool
	LookupSessionName   func(store beads.Store, cityName, qualifiedName, sessionTemplate string) string
	ScaleParams         func(a *config.Agent) ScaleInfo
	DefaultBranch       func(dir string) string
	PokeController      func(cityPath string) error
	PokeControlDispatch func(cityPath string) error
}

// OutputKind tags a result output line as a message or warning.
type OutputKind int

const (
	// OutputMessage is user-facing info (stdout in CLI).
	OutputMessage OutputKind = iota
	// OutputWarning is user-facing warning (stderr in CLI).
	OutputWarning
)

// OutputLine is a single line of output from a sling operation,
// preserving the interleaved order of messages and warnings.
type OutputLine struct {
	Kind OutputKind
	Text string
}

// SlingResult holds the structured output of a sling operation.
// Callers (CLI, API) format this for their respective surfaces.
type SlingResult struct {
	BeadID     string       // the routed bead ID (or wisp root for formula)
	Target     string       // qualified agent name
	Method     string       // "bead", "formula", "on-formula", "default-on-formula"
	WorkflowID string       // non-empty for graph workflow launches
	ConvoyID   string       // non-empty if auto-convoy was created
	Idempotent bool         // true if bead was already routed (skipped)
	Output     []OutputLine // ordered messages and warnings (preserves interleaving)

	// Batch fields (populated by DoSlingBatch).
	Routed     int
	Failed     int
	Skipped    int
	Total      int
	NudgeAgent *config.Agent // non-nil if caller should nudge
}

// Messages returns all message-kind output lines.
func (r SlingResult) Messages() []string {
	var msgs []string
	for _, o := range r.Output {
		if o.Kind == OutputMessage {
			msgs = append(msgs, o.Text)
		}
	}
	return msgs
}

// Warnings returns all warning-kind output lines.
func (r SlingResult) Warnings() []string {
	var warns []string
	for _, o := range r.Output {
		if o.Kind == OutputWarning {
			warns = append(warns, o.Text)
		}
	}
	return warns
}

// msg appends a message to the result output.
func (r *SlingResult) msg(text string) {
	r.Output = append(r.Output, OutputLine{Kind: OutputMessage, Text: text})
}

// warn appends a warning to the result output.
func (r *SlingResult) warn(text string) {
	r.Output = append(r.Output, OutputLine{Kind: OutputWarning, Text: text})
}

// ScaleInfo holds pool scaling parameters for an agent.
type ScaleInfo struct {
	Min int
	Max int
}

// SlingRunner executes a shell command in the given directory with optional
// extra env vars and returns combined output.
type SlingRunner func(dir, command string, env map[string]string) (string, error)

// SlingTracef writes to the sling trace log if GC_SLING_TRACE is set.
func SlingTracef(format string, args ...any) {
	path := strings.TrimSpace(os.Getenv("GC_SLING_TRACE"))
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()                                                                                    //nolint:errcheck // best-effort trace log
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(format, args...)) //nolint:errcheck
}

// FindRigByPrefix finds a rig whose effective prefix matches (case-insensitive).
func FindRigByPrefix(cfg *config.City, prefix string) (config.Rig, bool) {
	lp := strings.ToLower(prefix)
	for _, r := range cfg.Rigs {
		if strings.ToLower(r.EffectivePrefix()) == lp {
			return r, true
		}
	}
	return config.Rig{}, false
}

// RigDirForBead resolves the rig directory for a bead ID by extracting
// the bead prefix and looking up the rig path.
func RigDirForBead(cfg *config.City, beadID string) string {
	bp := BeadPrefix(beadID)
	if bp == "" {
		return ""
	}
	if rig, ok := FindRigByPrefix(cfg, bp); ok {
		return rig.Path
	}
	return ""
}

// RigDirForAgent returns the rig directory for an agent by matching its Dir
// field to a rig Name.
func RigDirForAgent(cfg *config.City, a config.Agent) string {
	if a.Dir == "" {
		return ""
	}
	for _, r := range cfg.Rigs {
		if r.Name == a.Dir {
			return r.Path
		}
	}
	return ""
}

// SlingDirForBead returns the directory for sling command execution.
func SlingDirForBead(cfg *config.City, cityPath, beadID string) string {
	if dir := RigDirForBead(cfg, beadID); dir != "" {
		return dir
	}
	return cityPath
}

// BuildSlingCommand replaces {} in the sling query template with the bead ID.
// The bead ID is shell-quoted to prevent command injection.
func BuildSlingCommand(template, beadID string) string {
	return strings.ReplaceAll(template, "{}", shellquote.Quote(beadID))
}

// FormatBeadLabel formats a bead ID with optional title for display.
func FormatBeadLabel(id, title string) string {
	if title != "" {
		return id + " — " + fmt.Sprintf("%q", title)
	}
	return id
}

// BeadPrefix extracts the rig prefix from a bead ID (everything before
// the first hyphen followed by a digit, or the explicit prefix separator).
func BeadPrefix(beadID string) string {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return ""
	}
	// Check for explicit prefix separator "--"
	if idx := strings.Index(beadID, "--"); idx > 0 {
		return beadID[:idx]
	}
	// Check for prefix-NNN pattern (prefix followed by hyphen and digit)
	for i := 0; i < len(beadID)-1; i++ {
		if beadID[i] == '-' && beadID[i+1] >= '0' && beadID[i+1] <= '9' {
			if i > 0 {
				return beadID[:i]
			}
		}
	}
	return ""
}

// RigPrefixForAgent returns the rig prefix that an agent's rig uses for bead IDs.
func RigPrefixForAgent(a config.Agent, cfg *config.City) string {
	if a.Dir == "" || cfg == nil {
		return ""
	}
	for _, r := range cfg.Rigs {
		if r.Name == a.Dir {
			return r.EffectivePrefix()
		}
	}
	return ""
}

// CheckCrossRig returns a warning message if a rig-scoped agent receives
// a bead from a different rig. Returns "" if routing is safe.
func CheckCrossRig(beadID string, a config.Agent, cfg *config.City) string {
	if cfg == nil || a.Dir == "" {
		return ""
	}
	bp := BeadPrefix(beadID)
	if bp == "" {
		return ""
	}
	rp := RigPrefixForAgent(a, cfg)
	if rp == "" {
		return ""
	}
	if strings.EqualFold(bp, rp) {
		return ""
	}
	return fmt.Sprintf("cross-rig routing — bead %s (prefix %q) → agent %s (rig prefix %q)", beadID, bp, a.QualifiedName(), rp)
}

// BeadExistsInStore checks if a bead exists in the given store.
func BeadExistsInStore(store beads.Store, id string) bool {
	if store == nil {
		return false
	}
	_, err := store.Get(id)
	return err == nil
}

// LooksLikeBeadID reports whether a string looks like a bead ID.
func LooksLikeBeadID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Bead IDs are typically prefix-NNN or just NNN.
	// They don't contain spaces, slashes, or common text punctuation.
	if strings.ContainsAny(s, " \t\n/\\") {
		return false
	}
	// If it contains a digit, it's likely a bead ID.
	for _, c := range s {
		if c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}

// IsCustomSlingQuery reports whether the agent has a custom sling_query
// (not the default bd-based one).
func IsCustomSlingQuery(a config.Agent) bool {
	q := strings.TrimSpace(a.EffectiveSlingQuery())
	return q != "" && !strings.HasPrefix(q, "bd ")
}

// BeadPriorityOverride reads the priority from an existing bead for use
// as a priority override when creating child beads.
func BeadPriorityOverride(store BeadQuerier, beadID string) *int {
	if store == nil || beadID == "" {
		return nil
	}
	bead, err := store.Get(beadID)
	if err != nil {
		return nil
	}
	return ClonePriorityPtr(bead.Priority)
}

// ClonePriorityPtr returns a copy of an *int, or nil if nil.
func ClonePriorityPtr(v *int) *int {
	if v == nil {
		return nil
	}
	cloned := *v
	return &cloned
}

// BeadMetadataTarget walks the bead's parent chain looking for a "target"
// metadata value (used for branch targeting).
func BeadMetadataTarget(store beads.Store, beadID string) string {
	if store == nil || beadID == "" {
		return ""
	}

	seen := make(map[string]struct{}, 8)
	rootID := beadID
	for beadID != "" {
		if _, ok := seen[beadID]; ok {
			return ""
		}
		seen[beadID] = struct{}{}

		b, err := store.Get(beadID)
		if err != nil {
			return ""
		}
		if target := strings.TrimSpace(b.Metadata["target"]); target != "" {
			if beadID == rootID || b.Type == "convoy" {
				return target
			}
		}
		beadID = strings.TrimSpace(b.ParentID)
	}
	return ""
}

// SlingFormulaSearchPaths returns the formula search paths for the current
// sling context.
func SlingFormulaSearchPaths(deps SlingDeps, a config.Agent) []string {
	if deps.Cfg == nil {
		return nil
	}
	return deps.Cfg.FormulaLayers.SearchPaths(a.Dir)
}

// SlingFormulaUsesBaseBranch reports whether the formula conventionally
// uses a base_branch variable.
func SlingFormulaUsesBaseBranch(formulaName string) bool {
	return strings.HasPrefix(formulaName, "mol-polecat-") || formulaName == "mol-scoped-work"
}

// SlingFormulaUsesTargetBranch reports whether the formula conventionally
// uses a target_branch variable.
func SlingFormulaUsesTargetBranch(formulaName string) bool {
	return formulaName == "mol-refinery-patrol"
}

// SlingFormulaRepoDir returns the best repo directory for formula variable
// resolution.
func SlingFormulaRepoDir(beadID string, deps SlingDeps, a config.Agent) string {
	if deps.Cfg != nil {
		if dir := RigDirForBead(deps.Cfg, beadID); dir != "" {
			return dir
		}
		if dir := RigDirForAgent(deps.Cfg, a); dir != "" {
			return dir
		}
	}
	return deps.CityPath
}

// SlingFormulaTargetBranch resolves the target branch for formula variables.
func SlingFormulaTargetBranch(beadID string, deps SlingDeps, a config.Agent) string {
	if target := BeadMetadataTarget(deps.Store, beadID); target != "" {
		return target
	}
	if deps.DefaultBranch != nil {
		return deps.DefaultBranch(SlingFormulaRepoDir(beadID, deps, a))
	}
	return ""
}

// BuildSlingFormulaVars builds the variable map for formula instantiation.
func BuildSlingFormulaVars(formulaName, beadID string, userVars []string, a config.Agent, deps SlingDeps) map[string]string {
	vars := make(map[string]string, len(userVars)+3)
	for _, v := range userVars {
		key, value, ok := strings.Cut(v, "=")
		if ok && key != "" {
			vars[key] = value
		}
	}
	addVar := func(key, value string) {
		if value == "" {
			return
		}
		if _, explicit := vars[key]; explicit {
			return
		}
		vars[key] = value
	}

	if beadID != "" {
		addVar("issue", beadID)
	}

	autoBranch := SlingFormulaTargetBranch(beadID, deps, a)
	if SlingFormulaUsesBaseBranch(formulaName) {
		addVar("base_branch", autoBranch)
	}
	if SlingFormulaUsesTargetBranch(formulaName) {
		addVar("target_branch", autoBranch)
	}

	return vars
}

// ResolveSlingEnv returns extra env vars for the sling command.
func ResolveSlingEnv(a config.Agent, deps SlingDeps) map[string]string {
	if deps.IsMultiSession != nil && deps.IsMultiSession(&a) {
		return nil
	}
	if deps.LookupSessionName == nil {
		return nil
	}
	sn := deps.LookupSessionName(deps.Store, deps.CityName, a.QualifiedName(), deps.Cfg.Workspace.SessionTemplate)
	return map[string]string{"GC_SLING_TARGET": sn}
}

// TargetType returns a human-readable label for the agent type.
func TargetType(a *config.Agent) string {
	if a == nil {
		return "unknown"
	}
	if a.MaxActiveSessions != nil && *a.MaxActiveSessions != 1 {
		return "pool"
	}
	return "agent"
}

// WorkflowStoreRefForDir maps a store directory to a "city:<name>" or
// "rig:<name>" store ref string.
func WorkflowStoreRefForDir(storeDir, cityPath, cityName string, cfg *config.City) string {
	if strings.TrimSpace(storeDir) == "" || strings.TrimSpace(cityPath) == "" {
		return ""
	}
	storeDir = NormalizePathForCompare(storeDir)
	cityPath = NormalizePathForCompare(cityPath)
	if storeDir == cityPath {
		cityName = strings.TrimSpace(cityName)
		if cityName == "" {
			cityName = "city"
		}
		return "city:" + cityName
	}
	if cfg == nil {
		return ""
	}
	for _, rig := range cfg.Rigs {
		rigPath := rig.Path
		if !filepath.IsAbs(rigPath) {
			rigPath = filepath.Join(cityPath, rigPath)
		}
		if SamePath(rigPath, storeDir) {
			return "rig:" + rig.Name
		}
	}
	return ""
}

// IsGraphWorkflowAttachment checks whether a bead is a graph.v2 workflow root.
func IsGraphWorkflowAttachment(store beads.Store, rootID string) bool {
	if store == nil || rootID == "" {
		return false
	}
	b, err := store.Get(rootID)
	if err != nil {
		return false
	}
	return b.Metadata["gc.kind"] == "workflow" && b.Metadata["gc.formula_contract"] == "graph.v2"
}

// InstantiateSlingFormula compiles and instantiates a formula, applying
// graph routing if the formula is a graph.v2 workflow.
func InstantiateSlingFormula(ctx context.Context, formulaName string, searchPaths []string, opts molecule.Options, sourceBeadID, scopeKind, scopeRef string, a config.Agent, deps SlingDeps) (*molecule.Result, error) {
	SlingTracef("instantiate start formula=%s source=%s agent=%s parent=%s", formulaName, sourceBeadID, a.QualifiedName(), opts.ParentID)
	if opts.PriorityOverride == nil && sourceBeadID != "" {
		opts.PriorityOverride = BeadPriorityOverride(deps.Store, sourceBeadID)
	}
	compileStart := time.Now()
	recipe, err := formula.Compile(ctx, formulaName, searchPaths, opts.Vars)
	if err != nil {
		SlingTracef("instantiate compile-error formula=%s dur=%s err=%v", formulaName, time.Since(compileStart), err)
		return nil, err
	}
	SlingTracef("instantiate compiled formula=%s dur=%s steps=%d", formulaName, time.Since(compileStart), len(recipe.Steps))
	if err := ApplyGraphRouting(recipe, &a, a.QualifiedName(), opts.Vars, sourceBeadID, scopeKind, scopeRef, deps.StoreRef, deps.Store, deps.CityName, deps.Cfg, deps); err != nil {
		SlingTracef("instantiate decorate-error formula=%s err=%v", formulaName, err)
		return nil, err
	}
	instantiateStart := time.Now()
	result, err := molecule.Instantiate(ctx, deps.Store, recipe, opts)
	if err != nil {
		SlingTracef("instantiate molecule-error formula=%s dur=%s err=%v", formulaName, time.Since(instantiateStart), err)
		return nil, err
	}
	SlingTracef("instantiate done formula=%s dur=%s root=%s created=%d graph=%t", formulaName, time.Since(instantiateStart), result.RootID, result.Created, result.GraphWorkflow)
	return result, nil
}

// ShouldPromoteWorkflowLaunchStatus reports whether a bead's status should
// be promoted to in_progress when a workflow launches.
func ShouldPromoteWorkflowLaunchStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "open", "ready", "todo", "triage", "backlog":
		return true
	default:
		return false
	}
}

// PromoteWorkflowLaunchBead sets a bead to in_progress if its current status
// is eligible for promotion.
func PromoteWorkflowLaunchBead(store beads.Store, beadID string) error {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return nil
	}
	bead, err := store.Get(beadID)
	if err != nil {
		return err
	}
	if !ShouldPromoteWorkflowLaunchStatus(bead.Status) {
		return nil
	}
	status := "in_progress"
	return store.Update(beadID, beads.UpdateOpts{Status: &status})
}

// StartGraphWorkflow is now inlined as doStartGraphWorkflow in sling_core.go.
// This function is kept as a deprecated alias for any remaining callers.
// Remove once all callers are updated.
func StartGraphWorkflow(result *molecule.Result, sourceBeadID string, a config.Agent, method string, deps SlingDeps) (SlingResult, error) {
	return doStartGraphWorkflow(result, sourceBeadID, a, method, deps)
}

// BeadCheckResult holds the result of pre-flight bead state checks.
type BeadCheckResult struct {
	Idempotent bool
	Warnings   []string
}
