package api

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"context"
	"os"
	"os/exec"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/ops"
)

type slingBody struct {
	Rig            string            `json:"rig"`
	Target         string            `json:"target"`
	Bead           string            `json:"bead"`
	Formula        string            `json:"formula"`
	AttachedBeadID string            `json:"attached_bead_id"`
	Title          string            `json:"title"`
	Vars           map[string]string `json:"vars"`
	ScopeKind      string            `json:"scope_kind"`
	ScopeRef       string            `json:"scope_ref"`
}

type slingResponse struct {
	Status         string `json:"status"`
	Target         string `json:"target"`
	Formula        string `json:"formula,omitempty"`
	Bead           string `json:"bead,omitempty"`
	WorkflowID     string `json:"workflow_id,omitempty"`
	RootBeadID     string `json:"root_bead_id,omitempty"`
	AttachedBeadID string `json:"attached_bead_id,omitempty"`
	Mode           string `json:"mode,omitempty"`
}

func (s *Server) handleSling(w http.ResponseWriter, r *http.Request) {
	var body slingBody
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if body.Target == "" {
		writeError(w, http.StatusBadRequest, "invalid", "target agent or pool is required")
		return
	}

	cfg := s.state.Config()
	agentCfg, ok := findAgent(cfg, body.Target)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "target "+body.Target+" not found")
		return
	}

	if body.Bead == "" && body.Formula == "" {
		writeError(w, http.StatusBadRequest, "invalid", "bead or formula is required")
		return
	}
	if body.Bead != "" && body.Formula != "" {
		writeError(w, http.StatusBadRequest, "invalid", "bead and formula are mutually exclusive")
		return
	}
	if body.Bead != "" && body.AttachedBeadID != "" {
		writeError(w, http.StatusBadRequest, "invalid", "bead and attached_bead_id are mutually exclusive")
		return
	}

	body.ScopeKind = strings.TrimSpace(body.ScopeKind)
	body.ScopeRef = strings.TrimSpace(body.ScopeRef)
	workflowLaunchOptions := body.AttachedBeadID != "" ||
		len(body.Vars) > 0 ||
		body.Title != "" ||
		body.ScopeKind != "" ||
		body.ScopeRef != ""
	defaultFormulaLaunch := body.Formula == "" &&
		body.AttachedBeadID == "" &&
		body.Bead != "" &&
		agentCfg.EffectiveDefaultSlingFormula() != "" &&
		(len(body.Vars) > 0 || body.Title != "" || body.ScopeKind != "" || body.ScopeRef != "")
	if body.Formula == "" && body.AttachedBeadID != "" {
		writeError(w, http.StatusBadRequest, "invalid", "formula is required when attached_bead_id is provided")
		return
	}
	if body.Formula == "" && workflowLaunchOptions && !defaultFormulaLaunch {
		writeError(w, http.StatusBadRequest, "invalid", "formula or target default formula is required when vars, title, or scope are provided")
		return
	}
	if (body.ScopeKind == "") != (body.ScopeRef == "") {
		writeError(w, http.StatusBadRequest, "invalid", "scope_kind and scope_ref must be provided together")
		return
	}
	if body.ScopeKind != "" && body.ScopeKind != "city" && body.ScopeKind != "rig" {
		writeError(w, http.StatusBadRequest, "invalid", "scope_kind must be 'city' or 'rig'")
		return
	}

	resp, status, code, message := s.execSlingDirect(body, agentCfg)
	if code != "" {
		writeError(w, status, code, message)
		return
	}
	writeJSON(w, status, resp)
}

// execSlingDirect calls ops.DoSling directly instead of shelling out.
func (s *Server) execSlingDirect(body slingBody, agentCfg config.Agent) (*slingResponse, int, string, string) {
	formulaName := strings.TrimSpace(body.Formula)
	attachedBeadID := strings.TrimSpace(body.AttachedBeadID)
	mode := "direct"
	workflowLaunch := false

	// Build SlingOpts from request body.
	slingOpts := ops.SlingOpts{
		Target:   agentCfg,
		SkipPoke: false,
	}

	// Build vars slice from map (sorted for determinism).
	var varSlice []string
	if len(body.Vars) > 0 {
		keys := make([]string, 0, len(body.Vars))
		for k := range body.Vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			varSlice = append(varSlice, k+"="+body.Vars[k])
		}
	}
	slingOpts.Vars = varSlice

	switch {
	case attachedBeadID != "":
		mode = "attached"
		workflowLaunch = true
		slingOpts.BeadOrFormula = attachedBeadID
		slingOpts.OnFormula = formulaName
	case formulaName != "":
		mode = "standalone"
		workflowLaunch = true
		slingOpts.BeadOrFormula = formulaName
		slingOpts.IsFormula = true
	case strings.TrimSpace(body.Bead) != "" &&
		agentCfg.EffectiveDefaultSlingFormula() != "" &&
		(len(body.Vars) > 0 || body.Title != "" || body.ScopeKind != "" || body.ScopeRef != ""):
		mode = "attached"
		workflowLaunch = true
		attachedBeadID = strings.TrimSpace(body.Bead)
		formulaName = agentCfg.EffectiveDefaultSlingFormula()
		slingOpts.BeadOrFormula = attachedBeadID
		// Default formula is applied automatically by DoSling when no --on/--formula.
	default:
		slingOpts.BeadOrFormula = body.Bead
	}

	if workflowLaunch {
		slingOpts.Title = strings.TrimSpace(body.Title)
		slingOpts.ScopeKind = body.ScopeKind
		slingOpts.ScopeRef = body.ScopeRef
	}

	// Build SlingDeps from api.State.
	store := s.findSlingStore(body.Rig, agentCfg)
	var stdout, stderr bytes.Buffer
	deps := ops.SlingDeps{
		CityName: s.state.CityName(),
		CityPath: s.state.CityPath(),
		Cfg:      s.state.Config(),
		SP:       s.state.SessionProvider(),
		Store:    store,
		StoreRef: s.slingStoreRef(body.Rig, agentCfg),
		Runner:   s.slingRunner(),
		Stdout:   &stdout,
		Stderr:   &stderr,
		// Inject API-side resolution functions.
		ResolveAgent: func(cfg *config.City, name, rigContext string) (config.Agent, bool) {
			return findAgent(cfg, name)
		},
		IsMultiSession: func(a *config.Agent) bool {
			if a == nil {
				return false
			}
			maxSess := a.EffectiveMaxActiveSessions()
			return maxSess == nil || *maxSess != 1
		},
		LookupSessionName: apiLookupSessionName,
		PokeController: func(_ string) error {
			s.state.Poke()
			return nil
		},
	}

	// Call ops.DoSling directly.
	exitCode := ops.DoSling(slingOpts, deps, store)
	if exitCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = fmt.Sprintf("sling failed with exit code %d", exitCode)
		}
		return nil, http.StatusBadRequest, "invalid", message
	}

	resp := &slingResponse{
		Status: "slung",
		Target: body.Target,
		Bead:   body.Bead,
		Mode:   mode,
	}
	if !workflowLaunch {
		return resp, http.StatusOK, "", ""
	}

	resp.Formula = formulaName
	resp.AttachedBeadID = attachedBeadID
	workflowID := parseWorkflowIDFromSlingOutput(stdout.String())
	if workflowID == "" {
		workflowID = parseWorkflowIDFromSlingOutput(stderr.String())
	}
	if workflowID == "" {
		return nil, http.StatusInternalServerError, "internal", "sling did not report a workflow id"
	}
	resp.WorkflowID = workflowID
	resp.RootBeadID = workflowID
	return resp, http.StatusCreated, "", ""
}

// findSlingStore returns the bead store for sling operations.
func (s *Server) findSlingStore(rig string, agentCfg config.Agent) beads.Store {
	if rig != "" {
		if store := s.state.BeadStore(rig); store != nil {
			return store
		}
	}
	if agentCfg.Dir != "" {
		if store := s.state.BeadStore(agentCfg.Dir); store != nil {
			return store
		}
	}
	return s.state.CityBeadStore()
}

// slingStoreRef returns a store ref string for the sling context.
func (s *Server) slingStoreRef(rig string, agentCfg config.Agent) string {
	if rig != "" {
		return "rig:" + rig
	}
	if agentCfg.Dir != "" {
		return "rig:" + agentCfg.Dir
	}
	return "city:" + s.state.CityName()
}

// slingRunner returns the SlingRunner for the API context.
// Uses SlingRunnerFunc if set (for tests), otherwise a real shell runner.
func (s *Server) slingRunner() ops.SlingRunner {
	if s.SlingRunnerFunc != nil {
		return s.SlingRunnerFunc
	}
	return func(dir, command string, env map[string]string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		if dir != "" {
			cmd.Dir = dir
		}
		if len(env) > 0 {
			cmd.Env = mergeEnvForSling(env)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("running %q: %w", command, err)
		}
		return string(out), nil
	}
}

// mergeEnvForSling merges extra env vars into the current process env.
func mergeEnvForSling(extra map[string]string) []string {
	base := os.Environ()
	merged := make([]string, 0, len(base)+len(extra))
	merged = append(merged, base...)
	for k, v := range extra {
		merged = append(merged, k+"="+v)
	}
	return merged
}

// apiLookupSessionName resolves a session name from the bead store.
func apiLookupSessionName(store beads.Store, cityName, qualifiedName, sessionTemplate string) string {
	return agent.SessionNameFor(cityName, qualifiedName, sessionTemplate)
}

func parseWorkflowIDFromSlingOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Started workflow ", "Attached workflow "} {
			if rest, ok := strings.CutPrefix(line, prefix); ok {
				workflowID, _, _ := strings.Cut(rest, " ")
				return strings.TrimSpace(workflowID)
			}
		}
		if rest, ok := strings.CutPrefix(line, "Slung formula "); ok {
			if _, afterRoot, found := strings.Cut(rest, "(wisp root "); found {
				workflowID, _, _ := strings.Cut(afterRoot, ")")
				return strings.TrimSpace(workflowID)
			}
		}
	}
	return ""
}
