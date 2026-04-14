package graphroute

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/formula"
)

func TestIsCompiledGraphWorkflow(t *testing.T) {
	t.Run("nil recipe", func(t *testing.T) {
		if IsCompiledGraphWorkflow(nil) {
			t.Error("expected false for nil recipe")
		}
	})
	t.Run("empty steps", func(t *testing.T) {
		if IsCompiledGraphWorkflow(&formula.Recipe{}) {
			t.Error("expected false for empty steps")
		}
	})
	t.Run("graph workflow", func(t *testing.T) {
		r := &formula.Recipe{
			Steps: []formula.RecipeStep{{
				Metadata: map[string]string{
					"gc.kind":             "workflow",
					"gc.formula_contract": "graph.v2",
				},
			}},
		}
		if !IsCompiledGraphWorkflow(r) {
			t.Error("expected true for graph.v2 workflow")
		}
	})
}

func TestIsControlDispatcherKind(t *testing.T) {
	for _, kind := range []string{"check", "fanout", "retry-eval", "scope-check", "workflow-finalize", "retry", "ralph"} {
		if !IsControlDispatcherKind(kind) {
			t.Errorf("expected true for %q", kind)
		}
	}
	if IsControlDispatcherKind("task") {
		t.Error("expected false for task")
	}
}

func TestGraphRouteRigContext(t *testing.T) {
	if got := GraphRouteRigContext("myrig/worker"); got != "myrig" {
		t.Errorf("got %q, want myrig", got)
	}
	if got := GraphRouteRigContext("mayor"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestGraphWorkflowRouteVars(t *testing.T) {
	dflt := "default-val"
	r := &formula.Recipe{
		Vars: map[string]*formula.VarDef{
			"base": {Default: &dflt},
		},
	}
	got := GraphWorkflowRouteVars(r, map[string]string{"override": "yes"})
	if got["base"] != "default-val" {
		t.Errorf("base = %q, want default-val", got["base"])
	}
	if got["override"] != "yes" {
		t.Errorf("override = %q, want yes", got["override"])
	}
}

func intPtr(v int) *int { return &v }

func TestApplyGraphRouting_NonGraph(t *testing.T) {
	// Non-graph recipe should be a no-op.
	r := &formula.Recipe{
		Steps: []formula.RecipeStep{{
			Metadata: map[string]string{"gc.kind": "task"},
		}},
	}
	a := config.Agent{Name: "worker", MaxActiveSessions: intPtr(1)}
	err := ApplyGraphRouting(r, &a, "worker", nil, "", "", "", "", nil, "city", &config.City{}, Deps{})
	if err != nil {
		t.Fatalf("unexpected error for non-graph recipe: %v", err)
	}
}
