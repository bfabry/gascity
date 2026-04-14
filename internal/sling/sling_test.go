package sling

import (
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

func testIsMultiSession(a *config.Agent) bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.Namepool) != "" || len(a.NamepoolNames) > 0 {
		return true
	}
	maxSess := a.EffectiveMaxActiveSessions()
	return maxSess == nil || *maxSess != 1
}

func testLookupSessionName(_ beads.Store, cityName, qualifiedName, _ string) string {
	return cityName + "-" + qualifiedName
}

func testDeps(cfg *config.City, sp runtime.Provider, runner SlingRunner) SlingDeps {
	if cfg != nil && len(cfg.FormulaLayers.City) == 0 {
		cfg.FormulaLayers.City = []string{sharedTestFormulaDir}
	}
	return SlingDeps{
		CityName:          "test-city",
		CityPath:          "/city",
		Cfg:               cfg,
		SP:                sp,
		Runner:            runner,
		Store:             beads.NewMemStore(),
		StoreRef:          "city:test-city",
		IsMultiSession:    testIsMultiSession,
		LookupSessionName: testLookupSessionName,
		ScaleParams: func(a *config.Agent) ScaleInfo {
			max := 0
			if m := a.EffectiveMaxActiveSessions(); m != nil {
				max = *m
			}
			return ScaleInfo{Max: max}
		},
		PokeController: func(string) error { return nil },
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
	found := false
	for _, w := range result.Warnings() {
		if strings.Contains(w, "suspended") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected suspension warning in %v", result.Warnings())
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
