package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestBuildPrimeContextFallsBackToConfiguredRigRoot(t *testing.T) {
	t.Setenv("GC_RIG", "demo")
	t.Setenv("GC_RIG_ROOT", "")
	t.Setenv("GC_DIR", "/tmp/demo-work")
	t.Setenv("GC_BRANCH", "")

	ctx := buildPrimeContext("/city", &config.Agent{Name: "polecat", Dir: "demo"}, []config.Rig{
		{Name: "demo", Path: "/repos/demo", Prefix: "dm"},
	})

	if ctx.RigName != "demo" {
		t.Fatalf("RigName = %q, want demo", ctx.RigName)
	}
	if ctx.RigRoot != "/repos/demo" {
		t.Fatalf("RigRoot = %q, want /repos/demo", ctx.RigRoot)
	}
}

func TestBuildPrimeContextPrefersGCAliasOverGCAgent(t *testing.T) {
	// When GC_AGENT is a session bead ID, buildPrimeContext should prefer
	// GC_ALIAS for AgentName so the prompt doesn't contain a bead ID.
	t.Setenv("GC_AGENT", "bl-9jl")
	t.Setenv("GC_ALIAS", "mayor")
	t.Setenv("GC_RIG", "")
	t.Setenv("GC_DIR", "")
	t.Setenv("GC_BRANCH", "")

	ctx := buildPrimeContext("/city", &config.Agent{Name: "mayor"}, nil)

	if ctx.AgentName != "mayor" {
		t.Errorf("AgentName = %q, want %q (should prefer GC_ALIAS over GC_AGENT)", ctx.AgentName, "mayor")
	}
}

func TestBuildPrimeContextUsesAliasEvenWhenDifferentFromConfigName(t *testing.T) {
	// When GC_ALIAS is set but differs from the config agent name, AgentName
	// should still reflect GC_ALIAS — the alias is the public identity the
	// prompt should use.
	t.Setenv("GC_AGENT", "bl-9jl")
	t.Setenv("GC_ALIAS", "custom-alias")
	t.Setenv("GC_RIG", "")
	t.Setenv("GC_DIR", "")
	t.Setenv("GC_BRANCH", "")

	ctx := buildPrimeContext("/city", &config.Agent{Name: "mayor"}, nil)

	if ctx.AgentName != "custom-alias" {
		t.Errorf("AgentName = %q, want %q (should use GC_ALIAS even when it differs from config name)", ctx.AgentName, "custom-alias")
	}
}

func TestBuildPrimeContextFallsBackToGCAgentWhenNoAlias(t *testing.T) {
	// When GC_ALIAS is not set, buildPrimeContext should still use GC_AGENT.
	t.Setenv("GC_AGENT", "mayor")
	t.Setenv("GC_ALIAS", "")
	t.Setenv("GC_RIG", "")
	t.Setenv("GC_DIR", "")
	t.Setenv("GC_BRANCH", "")

	ctx := buildPrimeContext("/city", &config.Agent{Name: "mayor"}, nil)

	if ctx.AgentName != "mayor" {
		t.Errorf("AgentName = %q, want %q", ctx.AgentName, "mayor")
	}
}
