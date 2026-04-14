package sling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// --- Test helpers ---

type fakeRunnerRule struct {
	prefix string
	out    string
	err    error
}

type fakeRunner struct {
	calls []string
	dirs  []string
	envs  []map[string]string
	rules []fakeRunnerRule
}

func newFakeRunner() *fakeRunner { return &fakeRunner{} }

func (r *fakeRunner) on(prefix, out string, err error) {
	r.rules = append(r.rules, fakeRunnerRule{prefix: prefix, out: out, err: err})
}

func (r *fakeRunner) run(dir, command string, env map[string]string) (string, error) {
	r.calls = append(r.calls, command)
	r.dirs = append(r.dirs, dir)
	r.envs = append(r.envs, env)
	for _, rule := range r.rules {
		if strings.Contains(command, rule.prefix) {
			return rule.out, rule.err
		}
	}
	return "", nil
}

func intPtr(v int) *int { return &v }


// testResolver implements AgentResolver for tests using exact match.
type testResolver struct{}

func (testResolver) ResolveAgent(cfg *config.City, name, _ string) (config.Agent, bool) {
	for _, a := range cfg.Agents {
		if a.QualifiedName() == name || a.Name == name {
			return a, true
		}
	}
	return config.Agent{}, false
}

// testNotifier implements Notifier as a no-op.
type testNotifier struct{}

func (testNotifier) PokeController(_ string)      {}
func (testNotifier) PokeControlDispatch(_ string) {}

func testDeps(cfg *config.City, sp runtime.Provider, runner SlingRunner) SlingDeps {
	if cfg != nil && len(cfg.FormulaLayers.City) == 0 {
		cfg.FormulaLayers.City = []string{sharedTestFormulaDir}
	}
	return SlingDeps{
		CityName: "test-city",
		CityPath: "/city",
		Cfg:      cfg,
		SP:       sp,
		Runner:   runner,
		Store:    beads.NewMemStore(),
		StoreRef: "city:test-city",
		Resolver: testResolver{},
		Notify:   testNotifier{},
	}
}

func testOpts(a config.Agent, beadOrFormula string) SlingOpts {
	return SlingOpts{Target: a, BeadOrFormula: beadOrFormula}
}

var sharedTestFormulaDir string

func init() {
	dir, err := os.MkdirTemp("", "gc-sling-test-formulas-*")
	if err != nil {
		panic(err)
	}
	for _, name := range []string{
		"code-review", "mol-feature", "mol-polecat-work", "mol-do-work",
		"mol-refinery-patrol", "review", "build", "test-formula",
		"bad-formula", "mol-polecat-pr", "custom-formula",
		"mol-digest", "mol-cleanup", "mol-db-health", "mol-health-check",
		"my-formula", "convoy-formula",
	} {
		content := fmt.Sprintf("formula = %q\nversion = 1\n\n[[steps]]\nid = \"work\"\ntitle = \"Work\"\n", name)
		_ = os.WriteFile(filepath.Join(dir, name+".formula.toml"), []byte(content), 0o644)
	}
	sharedTestFormulaDir = dir
}

// --- Pure helper tests ---

func TestBuildSlingCommandSling(t *testing.T) {
	tests := []struct {
		template string
		beadID   string
		want     string
	}{
		{"bd update {} --set-metadata gc.routed_to=mayor", "BL-42", "bd update 'BL-42' --set-metadata gc.routed_to=mayor"},
		{"bd update {} --add-label=pool:hw/polecat", "XY-7", "bd update 'XY-7' --add-label=pool:hw/polecat"},
		{"custom {} script {}", "ID-1", "custom 'ID-1' script 'ID-1'"},
	}
	for _, tt := range tests {
		got := BuildSlingCommand(tt.template, tt.beadID)
		if got != tt.want {
			t.Errorf("BuildSlingCommand(%q, %q) = %q, want %q", tt.template, tt.beadID, got, tt.want)
		}
	}
}

func TestBeadPrefixSling(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"BL-42", "BL"},
		{"HW-1", "HW"},
		{"DEMO--42", "DEMO"},
		{"", ""},
		{"nohyphen", ""},
	}
	for _, tt := range tests {
		got := BeadPrefix(tt.id)
		if got != tt.want {
			t.Errorf("BeadPrefix(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestCheckCrossRigSling(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{
			{Name: "myrig", Path: "/myrig", Prefix: "BL"},
			{Name: "other", Path: "/other", Prefix: "OT"},
		},
	}

	t.Run("same rig allowed", func(t *testing.T) {
		a := config.Agent{Name: "worker", Dir: "myrig"}
		if msg := CheckCrossRig("BL-42", a, cfg); msg != "" {
			t.Errorf("expected no warning, got %q", msg)
		}
	})

	t.Run("different rig blocked", func(t *testing.T) {
		a := config.Agent{Name: "worker", Dir: "other"}
		if msg := CheckCrossRig("BL-42", a, cfg); msg == "" {
			t.Error("expected cross-rig warning")
		}
	})

	t.Run("city agent no block", func(t *testing.T) {
		a := config.Agent{Name: "mayor"}
		if msg := CheckCrossRig("BL-42", a, cfg); msg != "" {
			t.Errorf("expected no warning, got %q", msg)
		}
	})
}

// --- DoSling integration tests (structured result) ---

func TestDoSlingBeadToFixedAgent(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps := testDeps(cfg, sp, runner.run)
	result, err := DoSling(testOpts(a, "BL-42"), deps, nil)

	if err != nil {
		t.Fatalf("DoSling error: %v", err)
	}
	if result.BeadID != "BL-42" {
		t.Errorf("BeadID = %q, want BL-42", result.BeadID)
	}
	if result.Target != "mayor" {
		t.Errorf("Target = %q, want mayor", result.Target)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d runner calls, want 1", len(runner.calls))
	}
}

func TestDoSlingSuspendedAgentWarns(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), Suspended: true}

	deps := testDeps(cfg, sp, runner.run)
	result, err := DoSling(testOpts(a, "BL-42"), deps, nil)

	if err != nil {
		t.Fatalf("DoSling error: %v", err)
	}
	if !result.AgentSuspended {
		t.Error("expected AgentSuspended=true")
	}
}

func TestDoSlingRunnerError(t *testing.T) {
	runner := newFakeRunner()
	runner.on("bd update", "", fmt.Errorf("runner failed"))
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps := testDeps(cfg, sp, runner.run)
	_, err := DoSling(testOpts(a, "BL-42"), deps, nil)

	if err == nil {
		t.Fatal("expected error from runner failure")
	}
}

func TestDoSlingFormulaToAgent(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps := testDeps(cfg, sp, runner.run)
	result, err := DoSling(SlingOpts{
		Target:        a,
		BeadOrFormula: "code-review",
		IsFormula:     true,
	}, deps, nil)

	if err != nil {
		t.Fatalf("DoSling error: %v", err)
	}
	if result.Method != "formula" {
		t.Errorf("Method = %q, want formula", result.Method)
	}
	if result.BeadID == "" {
		t.Error("expected non-empty BeadID (wisp root)")
	}
}

func TestDoSlingCrossRigBlocks(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Rigs: []config.Rig{
			{Name: "myrig", Path: "/myrig", Prefix: "BL"},
			{Name: "other", Path: "/other", Prefix: "OT"},
		},
	}
	a := config.Agent{Name: "worker", Dir: "other", MaxActiveSessions: intPtr(1)}

	deps := testDeps(cfg, sp, runner.run)
	_, err := DoSling(testOpts(a, "BL-42"), deps, nil)

	if err == nil {
		t.Fatal("expected cross-rig error")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not have been called")
	}
}

func TestDoSlingIdempotent(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Title:    "test",
		Metadata: map[string]string{"gc.routed_to": "mayor"},
	})

	deps := testDeps(cfg, sp, runner.run)
	deps.Store = store
	result, err := DoSling(testOpts(a, b.ID), deps, store)

	if err != nil {
		t.Fatalf("DoSling error: %v", err)
	}
	if !result.Idempotent {
		t.Error("expected Idempotent=true")
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not have been called")
	}
}

func TestCheckBatchBurnOutputsWarn(t *testing.T) {
	store := beads.NewMemStoreFrom(0, []beads.Bead{
		{ID: "BL-2", Type: "task", Status: "open"},
		{ID: "MOL-1", Type: "molecule", Status: "open", ParentID: "BL-2"},
	}, nil)
	child := beads.Bead{ID: "BL-2", Status: "open", Assignee: ""}
	var result SlingResult
	// Pass store as both the store and querier (MemStore implements BeadChildQuerier)
	err := CheckBatchNoMoleculeChildren(store, []beads.Bead{child}, store, &result)
	t.Logf("err=%v autoburned=%d", err, len(result.AutoBurned))
	if len(result.AutoBurned) == 0 {
		t.Error("expected auto-burn")
	}
	if result.AutoBurned[0] != "MOL-1" {
		t.Errorf("AutoBurned[0] = %q, want MOL-1", result.AutoBurned[0])
	}
}

func TestDoSlingValidatesRequiredDeps(t *testing.T) {
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	opts := testOpts(a, "BL-42")

	t.Run("nil Cfg", func(t *testing.T) {
		deps := testDeps(nil, nil, nil)
		deps.Cfg = nil
		_, err := DoSling(opts, deps, nil)
		if err == nil || !strings.Contains(err.Error(), "Cfg") {
			t.Errorf("expected Cfg validation error, got %v", err)
		}
	})

	t.Run("nil Store", func(t *testing.T) {
		deps := testDeps(&config.City{}, nil, nil)
		deps.Store = nil
		_, err := DoSling(opts, deps, nil)
		if err == nil || !strings.Contains(err.Error(), "Store") {
			t.Errorf("expected Store validation error, got %v", err)
		}
	})

	t.Run("nil Runner", func(t *testing.T) {
		deps := testDeps(&config.City{}, nil, nil)
		deps.Runner = nil
		_, err := DoSling(opts, deps, nil)
		if err == nil || !strings.Contains(err.Error(), "Runner") {
			t.Errorf("expected Runner validation error, got %v", err)
		}
	})
}

// --- Intent-based API tests ---

func TestNewSlingValidates(t *testing.T) {
	_, err := New(SlingDeps{})
	if err == nil {
		t.Error("expected validation error for empty deps")
	}
}

func TestNewSlingValid(t *testing.T) {
	deps := testDeps(&config.City{Workspace: config.Workspace{Name: "test"}}, runtime.NewFake(), newFakeRunner().run)
	s, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Sling")
	}
}

func TestSlingRouteBead(t *testing.T) {
	runner := newFakeRunner()
	deps := testDeps(&config.City{Workspace: config.Workspace{Name: "test"}}, runtime.NewFake(), runner.run)
	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	result, err := s.RouteBead(context.Background(), "BL-42", a, RouteOpts{})
	if err != nil {
		t.Fatalf("RouteBead: %v", err)
	}
	if result.BeadID != "BL-42" {
		t.Errorf("BeadID = %q, want BL-42", result.BeadID)
	}
	if result.Target != "mayor" {
		t.Errorf("Target = %q, want mayor", result.Target)
	}
	if result.Method != "bead" {
		t.Errorf("Method = %q, want bead", result.Method)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d runner calls, want 1", len(runner.calls))
	}
}

func TestSlingLaunchFormula(t *testing.T) {
	runner := newFakeRunner()
	cfg := &config.City{Workspace: config.Workspace{Name: "test"}}
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	result, err := s.LaunchFormula(context.Background(), "code-review", a, FormulaOpts{})
	if err != nil {
		t.Fatalf("LaunchFormula: %v", err)
	}
	if result.Method != "formula" {
		t.Errorf("Method = %q, want formula", result.Method)
	}
	if result.FormulaName != "code-review" {
		t.Errorf("FormulaName = %q, want code-review", result.FormulaName)
	}
	if result.BeadID == "" {
		t.Error("expected non-empty BeadID")
	}
}

// --- Typed router tests ---

type fakeBeadRouter struct {
	routed []RouteRequest
}

func (r *fakeBeadRouter) Route(_ context.Context, req RouteRequest) error {
	r.routed = append(r.routed, req)
	return nil
}

func TestSlingRouteBeadWithTypedRouter(t *testing.T) {
	router := &fakeBeadRouter{}
	cfg := &config.City{Workspace: config.Workspace{Name: "test"}}
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.Router = router

	s, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}

	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	_, err = s.RouteBead(context.Background(), "BL-42", a, RouteOpts{})
	if err != nil {
		t.Fatalf("RouteBead: %v", err)
	}

	if len(router.routed) != 1 {
		t.Fatalf("got %d route calls, want 1", len(router.routed))
	}
	if router.routed[0].BeadID != "BL-42" {
		t.Errorf("BeadID = %q, want BL-42", router.routed[0].BeadID)
	}
	if router.routed[0].Target != "mayor" {
		t.Errorf("Target = %q, want mayor", router.routed[0].Target)
	}
}
