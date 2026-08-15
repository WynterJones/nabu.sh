package steering

import (
	"strings"

	"github.com/nabu-sh/nabu/internal/domain"
)

type ActionCategory string

const (
	CategoryRead      ActionCategory = "read"
	CategoryWork      ActionCategory = "work"
	CategoryPublish   ActionCategory = "publish"
	CategoryDangerous ActionCategory = "dangerous"
)

type PolicyDecision string

const (
	DecisionAllow PolicyDecision = "allow"
	DecisionAsk   PolicyDecision = "ask"
)

// ActionKind is intentionally finite. Natural-language classification belongs
// in Codex; the daemon must map the resulting kind through this allowlist.
type ActionKind string

const (
	ActionReadFiles                 ActionKind = "read_files"
	ActionInspectWebsite            ActionKind = "inspect_website"
	ActionInspectRepository         ActionKind = "inspect_repository"
	ActionReadMetrics               ActionKind = "read_metrics"
	ActionWebResearch               ActionKind = "web_research"
	ActionEditFiles                 ActionKind = "edit_files"
	ActionRunTests                  ActionKind = "run_tests"
	ActionRunScripts                ActionKind = "run_scripts"
	ActionCreateBranch              ActionKind = "create_branch"
	ActionCreateCommit              ActionKind = "create_commit"
	ActionGenerateDraft             ActionKind = "generate_draft"
	ActionGenerateReport            ActionKind = "generate_report"
	ActionMerge                     ActionKind = "merge"
	ActionDeployProduction          ActionKind = "deploy_production"
	ActionPublishContent            ActionKind = "publish_content"
	ActionSendExternalMessage       ActionKind = "send_external_message"
	ActionModifyProductionConfig    ActionKind = "modify_production_config"
	ActionDeleteProductionData      ActionKind = "delete_production_data"
	ActionModifyAuthentication      ActionKind = "modify_authentication"
	ActionModifyBilling             ActionKind = "modify_billing"
	ActionSpendMoney                ActionKind = "spend_money"
	ActionChangeSecurityCredential  ActionKind = "change_security_credentials"
	ActionDestructiveInfrastructure ActionKind = "destructive_infrastructure"
)

type Enforcement struct {
	Action   ActionKind     `json:"action"`
	Category ActionCategory `json:"category"`
	Decision PolicyDecision `json:"decision"`
	Known    bool           `json:"known"`
	Reason   string         `json:"reason"`
}

func DefaultPolicy() domain.Policy {
	return domain.Policy{Read: string(DecisionAllow), Work: string(DecisionAllow), Publish: string(DecisionAsk), Dangerous: string(DecisionAsk)}
}

// NormalizePolicy accepts common persisted aliases and fails closed for
// invalid values. Dangerous actions always remain approval-bound.
func NormalizePolicy(policy domain.Policy) domain.Policy {
	defaults := DefaultPolicy()
	policy.Read = string(normalizeConfiguredDecision(policy.Read, PolicyDecision(defaults.Read)))
	policy.Work = string(normalizeConfiguredDecision(policy.Work, PolicyDecision(defaults.Work)))
	policy.Publish = string(normalizeConfiguredDecision(policy.Publish, PolicyDecision(defaults.Publish)))
	policy.Dangerous = string(DecisionAsk)
	return policy
}

func ClassifyAction(kind ActionKind) (ActionCategory, bool) {
	kind = ActionKind(normalizeToken(string(kind)))
	switch kind {
	case ActionReadFiles, ActionInspectWebsite, ActionInspectRepository, ActionReadMetrics, ActionWebResearch:
		return CategoryRead, true
	case ActionEditFiles, ActionRunTests, ActionRunScripts, ActionCreateBranch, ActionCreateCommit, ActionGenerateDraft, ActionGenerateReport:
		return CategoryWork, true
	case ActionMerge, ActionDeployProduction, ActionPublishContent, ActionSendExternalMessage, ActionModifyProductionConfig:
		return CategoryPublish, true
	case ActionDeleteProductionData, ActionModifyAuthentication, ActionModifyBilling, ActionSpendMoney, ActionChangeSecurityCredential, ActionDestructiveInfrastructure:
		return CategoryDangerous, true
	default:
		return CategoryDangerous, false
	}
}

// EvaluateAction is daemon-authoritative: it accepts only durable policy and a
// finite action kind. Unknown actions and all Dangerous actions require review.
func EvaluateAction(policy domain.Policy, kind ActionKind) Enforcement {
	category, known := ClassifyAction(kind)
	if !known {
		return Enforcement{Action: kind, Category: CategoryDangerous, Decision: DecisionAsk, Known: false, Reason: "unknown actions require approval"}
	}
	policy = NormalizePolicy(policy)
	var decision PolicyDecision
	switch category {
	case CategoryRead:
		decision = PolicyDecision(policy.Read)
	case CategoryWork:
		decision = PolicyDecision(policy.Work)
	case CategoryPublish:
		decision = PolicyDecision(policy.Publish)
	case CategoryDangerous:
		decision = DecisionAsk
	}
	reason := "durable policy allows this action"
	if decision == DecisionAsk {
		reason = "durable policy requires approval"
	}
	return Enforcement{Action: ActionKind(normalizeToken(string(kind))), Category: category, Decision: decision, Known: true, Reason: reason}
}

func normalizeConfiguredDecision(value string, fallback PolicyDecision) PolicyDecision {
	switch normalizeToken(value) {
	case "allow", "allowed", "auto", "automatic", "automatically":
		return DecisionAllow
	case "ask", "approval", "approval_required", "require_approval", "deny", "denied", "never":
		return DecisionAsk
	default:
		return fallback
	}
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
