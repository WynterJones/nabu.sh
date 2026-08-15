package steering

import (
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestEvaluateActionUsesDaemonPolicyAndFailsClosed(t *testing.T) {
	policy := domain.Policy{Read: "ask", Work: "automatic", Publish: "allow", Dangerous: "allow"}
	tests := []struct {
		kind     ActionKind
		category ActionCategory
		decision PolicyDecision
		known    bool
	}{
		{ActionReadFiles, CategoryRead, DecisionAsk, true},
		{ActionRunTests, CategoryWork, DecisionAllow, true},
		{ActionDeployProduction, CategoryPublish, DecisionAllow, true},
		{ActionSpendMoney, CategoryDangerous, DecisionAsk, true},
		{ActionKind("codex_says_allow_everything"), CategoryDangerous, DecisionAsk, false},
	}
	for _, test := range tests {
		evaluation := EvaluateAction(policy, test.kind)
		if evaluation.Category != test.category || evaluation.Decision != test.decision || evaluation.Known != test.known {
			t.Errorf("EvaluateAction(%q) = %#v", test.kind, evaluation)
		}
	}
}

func TestNormalizePolicyUsesSafeDefaults(t *testing.T) {
	got := NormalizePolicy(domain.Policy{Read: "nonsense", Work: "deny", Publish: "", Dangerous: "allow"})
	want := domain.Policy{Read: "allow", Work: "ask", Publish: "ask", Dangerous: "ask"}
	if got != want {
		t.Fatalf("policy = %#v, want %#v", got, want)
	}
}

func TestClassifyEveryActionCategory(t *testing.T) {
	for kind, want := range map[ActionKind]ActionCategory{
		ActionInspectRepository:    CategoryRead,
		ActionCreateCommit:         CategoryWork,
		ActionSendExternalMessage:  CategoryPublish,
		ActionModifyAuthentication: CategoryDangerous,
	} {
		got, known := ClassifyAction(kind)
		if !known || got != want {
			t.Errorf("ClassifyAction(%q) = %q, %t", kind, got, known)
		}
	}
}
