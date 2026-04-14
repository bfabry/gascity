package agentutil

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/config"
)

// ExpandedAgent holds a single (possibly pool-expanded) agent identity
// with lower-level facts for callers to map to their own taxonomy.
type ExpandedAgent struct {
	QualifiedName string
	Rig           string
	Pool          string // non-empty for pool members
	Suspended     bool
	Provider      string
	Description   string
}

// SessionLister is the subset of runtime.Provider needed for pool discovery.
type SessionLister interface {
	ListRunning(prefix string) ([]string, error)
}

// ExpandAgents expands all config agents into their effective runtime agents.
// Fixed agents (max=1) produce one entry. Bounded pools produce max entries.
// Unlimited pools discover running instances via the session lister.
func ExpandAgents(agents []config.Agent, cityName, sessTmpl string, sp SessionLister) []ExpandedAgent {
	var result []ExpandedAgent
	for _, a := range agents {
		result = append(result, expandSingleAgent(a, cityName, sessTmpl, sp)...)
	}
	return result
}

func expandSingleAgent(a config.Agent, cityName, sessTmpl string, sp SessionLister) []ExpandedAgent {
	maxSess := a.EffectiveMaxActiveSessions()
	isMulti := maxSess == nil || *maxSess != 1

	if !isMulti {
		return []ExpandedAgent{{
			QualifiedName: a.QualifiedName(),
			Rig:           a.Dir,
			Suspended:     a.Suspended,
			Provider:      a.Provider,
			Description:   a.Description,
		}}
	}

	poolName := a.QualifiedName()

	// Unlimited: discover running instances via session prefix.
	isUnlimited := maxSess == nil || *maxSess < 0
	if isUnlimited && sp != nil {
		return discoverUnlimitedPool(a, poolName, cityName, sessTmpl, sp)
	}

	// Bounded: static enumeration.
	poolMax := 1
	if maxSess != nil && *maxSess > 1 {
		poolMax = *maxSess
	}

	var result []ExpandedAgent
	for i := 1; i <= poolMax; i++ {
		memberName := PoolInstanceName(a.Name, i, a)
		qn := memberName
		if a.Dir != "" {
			qn = a.Dir + "/" + memberName
		}
		result = append(result, ExpandedAgent{
			QualifiedName: qn,
			Rig:           a.Dir,
			Pool:          poolName,
			Suspended:     a.Suspended,
			Provider:      a.Provider,
			Description:   a.Description,
		})
	}
	return result
}

func discoverUnlimitedPool(a config.Agent, poolName, cityName, sessTmpl string, sp SessionLister) []ExpandedAgent {
	qnPrefix := a.Name + "-"
	if a.Dir != "" {
		qnPrefix = a.Dir + "/" + a.Name + "-"
	}
	snPrefix := agent.SessionNameFor(cityName, qnPrefix, sessTmpl)

	running, err := sp.ListRunning(snPrefix)
	if err != nil || len(running) == 0 {
		return nil
	}

	templatePrefix := agent.SessionNameFor(cityName, "", sessTmpl)
	var result []ExpandedAgent
	for _, sn := range running {
		qnSanitized := sn
		if templatePrefix != "" && strings.HasPrefix(qnSanitized, templatePrefix) {
			qnSanitized = qnSanitized[len(templatePrefix):]
		}
		qn := strings.ReplaceAll(qnSanitized, "--", "/")
		result = append(result, ExpandedAgent{
			QualifiedName: qn,
			Rig:           a.Dir,
			Pool:          poolName,
			Suspended:     a.Suspended,
			Provider:      a.Provider,
			Description:   a.Description,
		})
	}
	return result
}

// PoolInstanceName returns the display name for a pool member at the given slot.
// Uses namepool_names if configured, otherwise "{base}-{slot}".
func PoolInstanceName(base string, slot int, a config.Agent) string {
	maxSess := a.EffectiveMaxActiveSessions()
	isMultiInstance := maxSess != nil && (*maxSess > 1 || *maxSess < 0)
	if !isMultiInstance {
		return base
	}
	if slot >= 1 && slot <= len(a.NamepoolNames) {
		return a.NamepoolNames[slot-1]
	}
	return fmt.Sprintf("%s-%d", base, slot)
}
