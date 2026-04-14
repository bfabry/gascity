package sling

import (
	"bytes"
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

// testIsMultiSession mirrors the real isMultiSessionCfgAgent logic.
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

// testLookupSessionName returns a predictable session name for testing.
func testLookupSessionName(_ beads.Store, cityName, qualifiedName, _ string) string {
	return cityName + "-" + qualifiedName
}

// testDeps builds a SlingDeps with test defaults and injected callbacks.
func testDeps(cfg *config.City, sp runtime.Provider, runner SlingRunner) (SlingDeps, *bytes.Buffer, *bytes.Buffer) {
	if cfg != nil && len(cfg.FormulaLayers.City) == 0 {
		cfg.FormulaLayers.City = []string{sharedTestFormulaDir}
	}
	var stdout, stderr bytes.Buffer
	return SlingDeps{
		CityName: "test-city",
		CityPath: "/city",
		Cfg:      cfg,
		SP:       sp,
		Runner:   runner,
		Store:    beads.NewMemStore(),
		StoreRef: "city:test-city",
		Stdout:   &stdout,
		Stderr:   &stderr,
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
	}, &stdout, &stderr
}

func testOpts(a config.Agent, beadOrFormula string) SlingOpts {
	return SlingOpts{Target: a, BeadOrFormula: beadOrFormula}
}

var sharedTestFormulaDir string

func init() {
	dir, err := os.MkdirTemp("", "gc-ops-sling-test-formulas-*")
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

func TestBuildSlingCommandOps(t *testing.T) {
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

func TestBeadPrefixOps(t *testing.T) {
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

func TestCheckCrossRigOps(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{
			{Name: "myrig", Path: "/myrig", Prefix: "BL"},
			{Name: "other", Path: "/other", Prefix: "OT"},
		},
	}

	t.Run("same rig allowed", func(t *testing.T) {
		a := config.Agent{Name: "worker", Dir: "myrig"}
		msg := CheckCrossRig("BL-42", a, cfg)
		if msg != "" {
			t.Errorf("expected no warning, got %q", msg)
		}
	})

	t.Run("different rig blocked", func(t *testing.T) {
		a := config.Agent{Name: "worker", Dir: "other"}
		msg := CheckCrossRig("BL-42", a, cfg)
		if msg == "" {
			t.Error("expected cross-rig warning, got empty string")
		}
	})

	t.Run("city agent no block", func(t *testing.T) {
		a := config.Agent{Name: "mayor"}
		msg := CheckCrossRig("BL-42", a, cfg)
		if msg != "" {
			t.Errorf("expected no warning for city agent, got %q", msg)
		}
	})
}

func TestFormatBeadLabelOps(t *testing.T) {
	if got := FormatBeadLabel("BL-1", ""); got != "BL-1" {
		t.Errorf("got %q", got)
	}
	if got := FormatBeadLabel("BL-1", "Fix bug"); !strings.Contains(got, "BL-1") || !strings.Contains(got, "Fix bug") {
		t.Errorf("got %q", got)
	}
}

func TestTargetTypeOps(t *testing.T) {
	fixed := config.Agent{Name: "a", MaxActiveSessions: intPtr(1)}
	if got := TargetType(&fixed); got != "agent" {
		t.Errorf("fixed agent: got %q, want agent", got)
	}
	pool := config.Agent{Name: "b", MaxActiveSessions: intPtr(3)}
	if got := TargetType(&pool); got != "pool" {
		t.Errorf("pool: got %q, want pool", got)
	}
}

func TestWorkflowStoreRefForDirOps(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{
			{Name: "myrig", Path: "/rigs/myrig"},
		},
	}
	if got := WorkflowStoreRefForDir("/city", "/city", "test-city", cfg); got != "city:test-city" {
		t.Errorf("city dir: got %q", got)
	}
	if got := WorkflowStoreRefForDir("/rigs/myrig", "/city", "test-city", cfg); got != "rig:myrig" {
		t.Errorf("rig dir: got %q", got)
	}
	if got := WorkflowStoreRefForDir("/unknown", "/city", "test-city", cfg); got != "" {
		t.Errorf("unknown dir: got %q", got)
	}
}

func TestFindRigByPrefixOps(t *testing.T) {
	cfg := &config.City{
		Rigs: []config.Rig{
			{Name: "myrig", Path: "/myrig", Prefix: "BL"},
		},
	}
	rig, ok := FindRigByPrefix(cfg, "BL")
	if !ok || rig.Name != "myrig" {
		t.Errorf("expected myrig, got %v %v", rig, ok)
	}
	_, ok = FindRigByPrefix(cfg, "XX")
	if ok {
		t.Error("expected not found")
	}
}

func TestLooksLikeBeadIDOps(t *testing.T) {
	if !LooksLikeBeadID("BL-42") {
		t.Error("BL-42 should look like bead ID")
	}
	if LooksLikeBeadID("fix the login bug") {
		t.Error("natural text should not look like bead ID")
	}
	if LooksLikeBeadID("") {
		t.Error("empty string should not look like bead ID")
	}
}

// --- DoSling integration tests ---

func TestDoSlingBeadToFixedAgentOps(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps, stdout, stderr := testDeps(cfg, sp, runner.run)
	opts := testOpts(a, "BL-42")
	code := DoSling(opts, deps, nil)

	if code != 0 {
		t.Fatalf("DoSling returned %d, want 0; stderr: %s", code, stderr.String())
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d runner calls, want 1: %v", len(runner.calls), runner.calls)
	}
	want := "bd update 'BL-42' --set-metadata gc.routed_to=mayor"
	if runner.calls[0] != want {
		t.Errorf("runner call = %q, want %q", runner.calls[0], want)
	}
	if !strings.Contains(stdout.String(), "Slung BL-42") {
		t.Errorf("stdout = %q, want to contain 'Slung BL-42'", stdout.String())
	}
}

func TestDoSlingSuspendedAgentWarnsOps(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1), Suspended: true}

	deps, _, stderr := testDeps(cfg, sp, runner.run)
	opts := testOpts(a, "BL-42")
	code := DoSling(opts, deps, nil)

	if code != 0 {
		t.Fatalf("DoSling returned %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "suspended") {
		t.Errorf("stderr = %q, want to contain 'suspended'", stderr.String())
	}
}

func TestDoSlingRunnerErrorOps(t *testing.T) {
	runner := newFakeRunner()
	runner.on("bd update", "", fmt.Errorf("runner failed"))
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps, _, _ := testDeps(cfg, sp, runner.run)
	opts := testOpts(a, "BL-42")
	code := DoSling(opts, deps, nil)

	if code != 1 {
		t.Fatalf("DoSling returned %d, want 1", code)
	}
}

func TestDoSlingFormulaToAgentOps(t *testing.T) {
	runner := newFakeRunner()
	sp := runtime.NewFake()
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	deps, stdout, stderr := testDeps(cfg, sp, runner.run)
	opts := SlingOpts{
		Target:        a,
		BeadOrFormula: "code-review",
		IsFormula:     true,
	}
	code := DoSling(opts, deps, nil)

	if code != 0 {
		t.Fatalf("DoSling returned %d, want 0; stderr: %s", code, stderr.String())
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d runner calls, want 1", len(runner.calls))
	}
	if !strings.Contains(stdout.String(), "Slung formula") {
		t.Errorf("stdout = %q, want to contain 'Slung formula'", stdout.String())
	}
}

func TestDoSlingCrossRigBlocksOps(t *testing.T) {
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

	deps, _, stderr := testDeps(cfg, sp, runner.run)
	opts := testOpts(a, "BL-42")
	code := DoSling(opts, deps, nil)

	if code != 1 {
		t.Fatalf("DoSling returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "cross-rig") {
		t.Errorf("stderr = %q, want to contain 'cross-rig'", stderr.String())
	}
	if len(runner.calls) != 0 {
		t.Error("runner should not have been called")
	}
}
