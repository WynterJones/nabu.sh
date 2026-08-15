package steering

import (
	"strings"
	"testing"

	"github.com/nabu-sh/nabu/internal/domain"
)

func TestPacketAndValidationExposeOnlyTypedLocalAppActions(t *testing.T) {
	packet, err := BuildPacket(PacketRequest{
		DisplayName: "Nabu", WorkspaceRoot: "/workspace", Mission: domain.Mission{Statement: "Ship useful tools"}, Policy: DefaultPolicy(),
		ContextGateEnabled: true, ContextReady: true, UserMessage: "Start the toolbox",
		LocalApps: []LocalAppSummary{{ID: "app-1", Name: "Toolbox", Directory: "repos/toolbox", Command: []string{"npm", "run", "dev"}, Port: 4173, HealthPath: "/", Status: "stopped", URL: "http://127.0.0.1:4173"}},
	})
	if err != nil || !strings.Contains(packet, `"local_apps"`) || !strings.Contains(packet, "start_local_app") {
		t.Fatalf("packet missing local app state: err=%v\n%s", err, packet)
	}

	state := ValidationState{ContextGateEnabled: true, ContextReady: true, LocalApps: []LocalAppSummary{{ID: "app-1", Name: "Toolbox"}}}
	result, err := ValidateResult(Result{AssistantResponse: "Starting it now.", Effects: []Effect{{Type: EffectStartLocalApp, AppID: "app-1"}}}, state)
	if err != nil || result.Effects[0].AppID != "app-1" {
		t.Fatalf("validated = %#v, err=%v", result, err)
	}
	if _, err := ValidateResult(Result{AssistantResponse: "Starting it.", Effects: []Effect{{Type: EffectStartLocalApp, AppID: "invented"}}}, state); err == nil {
		t.Fatal("unknown app ID accepted")
	}
}

func TestCreateLocalAppRequiresBoundedArgvAndReposFolder(t *testing.T) {
	state := ValidationState{ContextGateEnabled: true, ContextReady: true}
	valid := Result{AssistantResponse: "Registered the app.", Effects: []Effect{{Type: EffectCreateLocalApp, LocalApp: &LocalAppChange{
		Name: "Toolbox", Directory: "repos/toolbox", Command: []string{"npm", "run", "dev"}, Port: 4173, HealthPath: "/health",
	}}}}
	if _, err := ValidateResult(valid, state); err != nil {
		t.Fatalf("valid local app rejected: %v", err)
	}
	for _, directory := range []string{"../outside", ".", "app", "repos"} {
		valid.Effects[0].LocalApp.Directory = directory
		if _, err := ValidateResult(valid, state); err == nil {
			t.Fatalf("invalid local app folder %q accepted", directory)
		}
	}
}
