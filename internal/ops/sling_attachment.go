package ops

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// BeadFromGetters tries multiple BeadQuerier implementations and returns
// the first successful result.
func BeadFromGetters(id string, getters ...BeadQuerier) (beads.Bead, bool) {
	for _, getter := range getters {
		if getter == nil {
			continue
		}
		b, err := getter.Get(id)
		if err == nil {
			return b, true
		}
	}
	return beads.Bead{}, false
}

// CollectAttachedBeads finds all molecule/workflow attachments for a parent bead.
func CollectAttachedBeads(parent beads.Bead, store beads.Store, childQuerier BeadChildQuerier) ([]beads.Bead, error) {
	var (
		attachments []beads.Bead
		firstErr    error
	)
	seen := make(map[string]struct{})

	addByID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || store == nil {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		attached, err := store.Get(id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		seen[id] = struct{}{}
		attachments = append(attachments, attached)
	}

	addByID(parent.Metadata["molecule_id"])
	addByID(parent.Metadata["workflow_id"])

	if childQuerier != nil {
		children, err := childQuerier.List(beads.ListQuery{
			ParentID: parent.ID,
			Sort:     beads.SortCreatedAsc,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			for _, child := range children {
				if !IsAttachedRoot(child) {
					continue
				}
				if _, ok := seen[child.ID]; ok {
					continue
				}
				seen[child.ID] = struct{}{}
				attachments = append(attachments, child)
			}
		}
	}

	return attachments, firstErr
}

// AttachmentLabel returns "workflow" or "molecule" based on the bead type.
func AttachmentLabel(b beads.Bead) string {
	if IsWorkflowAttachment(b) {
		return "workflow"
	}
	return "molecule"
}

// IsAttachedRoot reports whether a bead is a workflow or molecule root.
func IsAttachedRoot(b beads.Bead) bool {
	return IsWorkflowAttachment(b) || IsMoleculeAttachment(b)
}

// IsWorkflowAttachment reports whether a bead is a graph.v2 workflow attachment.
func IsWorkflowAttachment(b beads.Bead) bool {
	return strings.EqualFold(strings.TrimSpace(b.Metadata["gc.kind"]), "workflow") ||
		strings.EqualFold(strings.TrimSpace(b.Metadata["gc.formula_contract"]), "graph.v2")
}

// IsMoleculeAttachment reports whether a bead is a molecule attachment.
func IsMoleculeAttachment(b beads.Bead) bool {
	return strings.EqualFold(strings.TrimSpace(b.Type), "molecule")
}

// CheckNoMoleculeChildren returns an error if the bead already has an attached
// molecule or wisp child that is still open.
func CheckNoMoleculeChildren(q BeadQuerier, beadID string, store beads.Store, w io.Writer) error {
	parent, ok := BeadFromGetters(beadID, q, store)
	if !ok {
		return nil
	}
	parentUnassigned := strings.TrimSpace(parent.Assignee) == ""

	var childQuerier BeadChildQuerier
	if cq, ok := q.(BeadChildQuerier); ok {
		childQuerier = cq
	} else if cq, ok := any(store).(BeadChildQuerier); ok {
		childQuerier = cq
	}
	attachments, err := CollectAttachedBeads(parent, store, childQuerier)
	if err != nil && len(attachments) == 0 {
		return nil
	}

	for _, attached := range attachments {
		if attached.Status == "closed" {
			continue
		}
		if parentUnassigned && store != nil {
			if burnErr := store.Close(attached.ID); burnErr == nil {
				fmt.Fprintf(w, "Auto-burned stale %s %s on unassigned bead %s\n", AttachmentLabel(attached), attached.ID, beadID) //nolint:errcheck // best-effort
				continue
			}
		}
		return fmt.Errorf("bead %s already has attached %s %s", beadID, AttachmentLabel(attached), attached.ID)
	}
	return nil
}

// CheckBatchNoMoleculeChildren checks all open children for existing molecule
// attachments before any wisps are created.
func CheckBatchNoMoleculeChildren(q BeadChildQuerier, open []beads.Bead, store beads.Store, w io.Writer) error {
	var problems []string
	for _, child := range open {
		attachments, err := CollectAttachedBeads(child, store, q)
		if err != nil && len(attachments) == 0 {
			continue
		}
		childUnassigned := strings.TrimSpace(child.Assignee) == ""
		for _, attached := range attachments {
			if attached.Status == "closed" {
				continue
			}
			if childUnassigned && store != nil {
				if burnErr := store.Close(attached.ID); burnErr == nil {
					fmt.Fprintf(w, "Auto-burned stale %s %s on unassigned bead %s\n", AttachmentLabel(attached), attached.ID, child.ID) //nolint:errcheck // best-effort
					continue
				}
			}
			problems = append(problems, fmt.Sprintf("%s (has %s %s)", child.ID, AttachmentLabel(attached), attached.ID))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("cannot use --on: beads already have attached molecules: %s",
			strings.Join(problems, ", "))
	}
	return nil
}

// CheckBeadState checks whether a bead is already routed and returns a
// structured result. Best-effort: nil querier or query failure → empty result.
func CheckBeadState(q BeadQuerier, beadID string, a config.Agent, deps SlingDeps) BeadCheckResult {
	if q == nil {
		return BeadCheckResult{}
	}
	b, err := q.Get(beadID)
	if err != nil {
		return BeadCheckResult{}
	}

	if IsCustomSlingQuery(a) {
		var warnings []string
		if b.Assignee != "" {
			warnings = append(warnings, fmt.Sprintf("warning: bead %s already assigned to %q", beadID, b.Assignee))
		}
		if routedTo := strings.TrimSpace(b.Metadata["gc.routed_to"]); routedTo != "" {
			warnings = append(warnings, fmt.Sprintf("warning: bead %s already routed to %q", beadID, routedTo))
		}
		for _, l := range b.Labels {
			if strings.HasPrefix(l, "pool:") {
				warnings = append(warnings, fmt.Sprintf("warning: bead %s already has pool label %q", beadID, l))
			}
		}
		return BeadCheckResult{Warnings: warnings}
	}

	target := a.QualifiedName()
	if strings.TrimSpace(b.Metadata["gc.routed_to"]) == target {
		if b.Assignee == "" || b.Assignee == target {
			return BeadCheckResult{Idempotent: true}
		}
		return BeadCheckResult{
			Warnings: []string{fmt.Sprintf("warning: bead %s routed to %q but assigned to %q", beadID, target, b.Assignee)},
		}
	}

	isMulti := deps.IsMultiSession != nil && deps.IsMultiSession(&a)
	if !isMulti {
		if b.Assignee == target {
			return BeadCheckResult{Idempotent: true}
		}
		var warnings []string
		if b.Assignee != "" {
			warnings = append(warnings, fmt.Sprintf("warning: bead %s already assigned to %q", beadID, b.Assignee))
		}
		if routedTo := strings.TrimSpace(b.Metadata["gc.routed_to"]); routedTo != "" {
			warnings = append(warnings, fmt.Sprintf("warning: bead %s already routed to %q", beadID, routedTo))
		}
		for _, l := range b.Labels {
			if strings.HasPrefix(l, "pool:") {
				warnings = append(warnings, fmt.Sprintf("warning: bead %s already has pool label %q", beadID, l))
			}
		}
		return BeadCheckResult{Warnings: warnings}
	}

	if strings.TrimSpace(b.Metadata["gc.routed_to"]) == "" {
		poolLabel := "pool:" + target
		for _, l := range b.Labels {
			if l == poolLabel {
				return BeadCheckResult{Idempotent: true}
			}
		}
	}
	var warnings []string
	if b.Assignee != "" {
		warnings = append(warnings, fmt.Sprintf("warning: bead %s already assigned to %q", beadID, b.Assignee))
	}
	if routedTo := strings.TrimSpace(b.Metadata["gc.routed_to"]); routedTo != "" {
		warnings = append(warnings, fmt.Sprintf("warning: bead %s already routed to %q", beadID, routedTo))
	}
	for _, l := range b.Labels {
		if strings.HasPrefix(l, "pool:") {
			warnings = append(warnings, fmt.Sprintf("warning: bead %s already has pool label %q", beadID, l))
		}
	}
	return BeadCheckResult{Warnings: warnings}
}
