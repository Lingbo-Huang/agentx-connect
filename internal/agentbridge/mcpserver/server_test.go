package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

func TestSearchToolInjectsHostIdentityAndReturnsStructuredResult(t *testing.T) {
	backend := &fakeSearchBackend{response: protocol.SearchCapabilitiesResponse{
		ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionMatchesFound,
		NextAction: protocol.SearchNextActionSelectExactMatch, SafeSummary: "One eligible capability was found.",
		Matches: []protocol.CapabilityMatch{{
			CapabilityID: "marketing.brief", ServiceID: "service-marketing", ServiceVersionID: "service-marketing@1",
			DisplayName: "Marketing brief", Description: "Produces a checked marketing brief.", CapabilityCodes: []string{"marketing.brief"},
			BindingID: "binding-marketing", BindingVersion: "1.0.0", ExecutionMode: protocol.ExecutionModeInvoke,
			Provider:      protocol.ProviderSummary{ProviderID: "provider.agentx", DisplayName: "AgentX", Kind: "AGENTX_OFFICIAL"},
			ArtifactTypes: []string{"text/markdown"}, VerifierKinds: []string{"citation.check"},
			ConnectionState: protocol.ConnectionStateReady, GrantState: protocol.GrantStateGranted,
		}},
	}}
	caller := testCaller()
	server, err := New(Config{Caller: caller, Search: backend})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session := connect(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != string(protocol.ToolSearchCapabilities) {
		t.Fatalf("tools = %#v", listed.Tools)
	}
	if listed.Tools[0].Annotations == nil || !listed.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("search annotations = %#v", listed.Tools[0].Annotations)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolSearchCapabilities),
		Arguments: map[string]any{
			"query": "marketing brief", "expectedArtifactTypes": []string{"text/markdown"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("result = %#v", result)
	}
	if backend.request.Caller != caller || backend.request.Mode != protocol.RoutingModeBalanced || backend.request.MaximumSensitivity != protocol.SensitivityInternal {
		t.Fatalf("backend request = %#v", backend.request)
	}
	if backend.request.Query != "marketing brief" || backend.request.ContractVersion != protocol.ContractVersion {
		t.Fatalf("backend request = %#v", backend.request)
	}
}

func TestSearchToolRejectsBackendThatOmitsFallbackDisposition(t *testing.T) {
	server, err := New(Config{
		Caller: testCaller(), Search: &fakeSearchBackend{response: protocol.SearchCapabilitiesResponse{ContractVersion: protocol.ContractVersion}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connect(t, server).CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolSearchCapabilities), Arguments: map[string]any{"query": "external delivery"},
	})
	if err != nil || !result.IsError || result.StructuredContent != nil {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestCompleteBuildExposesExactlyEightTypedAndAnnotatedTools(t *testing.T) {
	server, err := New(Config{
		Caller: testCaller(), Search: &fakeSearchBackend{}, Invoke: &fakeInvokeBackend{}, Handoff: &fakeHandoffBackend{},
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := connect(t, server).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		readOnly  bool
		openWorld bool
	}{
		string(protocol.ToolSearchCapabilities): {readOnly: true, openWorld: false},
		string(protocol.ToolInvokeCapability):   {readOnly: false, openWorld: true},
		string(protocol.ToolHandoffWork):        {readOnly: false, openWorld: true},
		string(protocol.ToolListHandoffs):       {readOnly: true, openWorld: false},
		string(protocol.ToolGetHandoffStatus):   {readOnly: true, openWorld: false},
		string(protocol.ToolFetchArtifact):      {readOnly: true, openWorld: false},
		string(protocol.ToolAcceptDelivery):     {readOnly: false, openWorld: false},
		string(protocol.ToolRejectDelivery):     {readOnly: false, openWorld: false},
	}
	if len(listed.Tools) != len(expected) {
		t.Fatalf("tool count=%d, want %d: %#v", len(listed.Tools), len(expected), listed.Tools)
	}
	for _, tool := range listed.Tools {
		want, ok := expected[tool.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		if !objectSchema(tool.InputSchema) || !objectSchema(tool.OutputSchema) {
			t.Errorf("tool %s schemas input=%#v output=%#v", tool.Name, tool.InputSchema, tool.OutputSchema)
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != want.readOnly ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint != want.openWorld ||
			!tool.Annotations.IdempotentHint {
			t.Errorf("tool %s annotations=%#v", tool.Name, tool.Annotations)
		}
		if !want.readOnly && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			t.Errorf("tool %s must declare additive, non-destructive mutation", tool.Name)
		}
	}
}

func TestHostGuidancePreservesPassThroughInvokeAndHandoffBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		profile ToolProfile
		tools   map[string][]string
	}{
		{
			name: "full", profile: ToolProfileFull,
			tools: map[string][]string{
				string(protocol.ToolSearchCapabilities): {"ordinary work", "split work", "another responsibility owner"},
				string(protocol.ToolInvokeCapability):   {"short-lived", "InvocationReceipt", "never silently substitute a Runtime"},
				string(protocol.ToolHandoffWork):        {"crosses a Principal", "expected Artifacts", "HandoffRef"},
				string(protocol.ToolListHandoffs):       {"recent", "Host installations", "get_handoff_status"},
			},
		},
		{
			name: "compact", profile: ToolProfileCompact,
			tools: map[string][]string{
				string(protocol.ToolSearchCapabilities): {"ordinary work", "switch models", "durable cross-session work"},
				string(protocol.ToolUseCapability):      {"executionMode", "InvocationReceipt", "HandoffRef", "silently fall back to a Runtime"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, err := New(Config{
				Caller: testCaller(), Search: &fakeSearchBackend{}, Invoke: &fakeInvokeBackend{},
				Handoff: &fakeHandoffBackend{}, Profile: test.profile,
			})
			if err != nil {
				t.Fatal(err)
			}
			session := connect(t, server)
			initialization := session.InitializeResult()
			if initialization == nil {
				t.Fatal("MCP initialization result is missing")
			}
			for _, phrase := range []string{"PASS_THROUGH", "INVOKE", "HANDOFF", "HandoffRef", "Never silently fall back to a Runtime"} {
				if !strings.Contains(initialization.Instructions, phrase) {
					t.Errorf("server instructions lost %q: %s", phrase, initialization.Instructions)
				}
			}
			listed, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			descriptions := make(map[string]string, len(listed.Tools))
			for _, tool := range listed.Tools {
				descriptions[tool.Name] = tool.Description
			}
			for name, phrases := range test.tools {
				description, ok := descriptions[name]
				if !ok {
					t.Errorf("required tool %q is missing", name)
					continue
				}
				for _, phrase := range phrases {
					if !strings.Contains(description, phrase) {
						t.Errorf("tool %q description lost %q: %s", name, phrase, description)
					}
				}
			}
		})
	}
}

func TestCompactProfileExposesTwoToolsAndPreservesAllCompletionOperations(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	output := json.RawMessage(`{"valid":true}`)
	invoke := &fakeInvokeBackend{response: protocol.InvocationReceipt{
		ContractVersion: protocol.ContractVersion, InvocationID: "invocation-compact-1",
		BindingID: "binding-invoke", BindingVersion: "1.0.0", Status: protocol.InvocationStatusSucceeded,
		OutputMediaType: "application/json", Output: output, SafeSummary: "Invocation completed.", TraceID: "trace-compact-1",
		StartedAt: now, FinishedAt: timePointer(now.Add(time.Millisecond)), DurationMillis: 1,
		InputBytes: 2, OutputBytes: int64(len(output)),
	}}
	reference := protocol.HandoffRef{
		ContractVersion: protocol.ContractVersion, HandoffID: "handoff-compact-1", MissionID: "mission-compact-1",
		Status:            protocol.HandoffStatusInProgress,
		ExpectedArtifacts: []protocol.ArtifactExpectation{{LogicalName: "result.txt", MediaType: "text/plain"}},
		Provider:          protocol.ProviderSummary{ProviderID: "provider-1", DisplayName: "Provider One", Kind: "PLATFORM"},
		CreatedAt:         now, DeepLink: "/missions/mission-compact-1", PollAfterMillis: 1000,
	}
	handoff := &fakeHandoffBackend{
		reference: reference,
		list: protocol.ListHandoffsResponse{ContractVersion: protocol.ContractVersion, Items: []protocol.HandoffListItem{{
			HandoffRef: reference, SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, Next: protocol.ResumeDirectiveWait,
			SafeSummary: "The handoff is running.", UpdatedAt: now,
		}}},
		status: protocol.HandoffStatusView{
			HandoffRef: reference, SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, UpdateCursor: "sha256:" + strings.Repeat("c", 64), Changed: true,
			Resume:      protocol.HandoffResumePayload{Next: protocol.ResumeDirectiveWait},
			SafeSummary: "The handoff is running.", UpdatedAt: now,
		},
		artifact: protocol.ArtifactReference{
			ContractVersion: protocol.ContractVersion, ArtifactID: "artifact-compact-1", LogicalName: "result.txt", MediaType: "text/plain",
			ContentHash: "sha256:" + strings.Repeat("a", 64), ContentURI: "/api/v1/missions/mission-compact-1/artifacts/artifact-compact-1/content",
		},
		acceptance: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: reference.HandoffID, Status: protocol.HandoffStatusAccepted,
			MissionVersion: 3, RecordedAt: now,
		},
		rejection: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: reference.HandoffID, Status: protocol.HandoffStatusRecovery,
			MissionVersion: 3, RecordedAt: now,
		},
	}
	server, err := New(Config{
		Caller: testCaller(), Search: &fakeSearchBackend{}, Invoke: invoke, Handoff: handoff, Profile: ToolProfileCompact,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connect(t, server)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if len(listed.Tools) != 2 || !names[string(protocol.ToolSearchCapabilities)] || !names[string(protocol.ToolUseCapability)] {
		t.Fatalf("compact tools=%#v", listed.Tools)
	}

	cases := []struct {
		name      string
		arguments map[string]any
		kind      CompactResultKind
	}{
		{name: "invoke", kind: CompactResultInvocation, arguments: map[string]any{
			"operation": "INVOKE", "bindingId": "binding-invoke", "bindingVersion": "1.0.0", "idempotencyKey": "invoke-compact-1", "input": map[string]any{},
		}},
		{name: "handoff", kind: CompactResultHandoff, arguments: map[string]any{
			"operation": "HANDOFF", "bindingId": "binding-handoff", "bindingVersion": "1.0.0", "idempotencyKey": "handoff-compact-1",
			"goal": "Produce a verified result", "expectedArtifacts": []map[string]any{{"logicalName": "result.txt", "mediaType": "text/plain"}},
		}},
		{name: "list", kind: CompactResultHandoffList, arguments: map[string]any{
			"operation": "LIST_HANDOFFS", "limit": float64(5),
		}},
		{name: "status", kind: CompactResultStatus, arguments: map[string]any{
			"operation": "GET_STATUS", "handoffId": reference.HandoffID, "knownUpdateCursor": "sha256:" + strings.Repeat("b", 64),
		}},
		{name: "artifact", kind: CompactResultArtifact, arguments: map[string]any{
			"operation": "FETCH_ARTIFACT", "handoffId": reference.HandoffID, "artifactId": "artifact-compact-1",
		}},
		{name: "accept", kind: CompactResultDecision, arguments: map[string]any{
			"operation": "ACCEPT_DELIVERY", "handoffId": reference.HandoffID, "missionVersion": float64(2), "idempotencyKey": "accept-compact-1",
		}},
		{name: "reject", kind: CompactResultDecision, arguments: map[string]any{
			"operation": "REJECT_DELIVERY", "handoffId": reference.HandoffID, "missionVersion": float64(2), "idempotencyKey": "reject-compact-1", "reason": "Revise",
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: string(protocol.ToolUseCapability), Arguments: test.arguments})
			if err != nil || result.IsError {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var projected CompactToolResult
			if err := json.Unmarshal(encoded, &projected); err != nil || projected.Kind != test.kind {
				t.Fatalf("projected=%#v error=%v", projected, err)
			}
			if test.name == "status" && handoff.statusRequest.KnownUpdateCursor != "sha256:"+strings.Repeat("b", 64) {
				t.Fatalf("compact status request lost update cursor: %#v", handoff.statusRequest)
			}
		})
	}
	if invoke.request.Caller != testCaller() || handoff.handoffRequest.Caller != testCaller() ||
		handoff.listRequest.Caller != testCaller() || handoff.statusRequest.Caller != testCaller() || handoff.artifactRequest.Caller != testCaller() ||
		handoff.acceptanceRequest.Caller != testCaller() || handoff.rejectionRequest.Caller != testCaller() {
		t.Fatal("compact profile did not inject the authenticated Host identity into every operation")
	}
}

func TestCompactProfileRejectsAmbiguousOperationFields(t *testing.T) {
	invoke := &fakeInvokeBackend{}
	server, err := New(Config{
		Caller: testCaller(), Search: &fakeSearchBackend{}, Invoke: invoke, Handoff: &fakeHandoffBackend{}, Profile: ToolProfileCompact,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connect(t, server).CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolUseCapability), Arguments: map[string]any{
			"operation": "INVOKE", "bindingId": "binding-1", "bindingVersion": "1", "idempotencyKey": "key-1",
			"input": map[string]any{}, "handoffId": "unexpected-handoff",
		},
	})
	if err != nil || !result.IsError || invoke.request.BindingID != "" {
		t.Fatalf("result=%#v request=%#v error=%v", result, invoke.request, err)
	}
}

func objectSchema(schema any) bool {
	value, ok := schema.(map[string]any)
	return ok && value["type"] == "object"
}

func TestInvokeToolInjectsHostIdentityAndReturnsReceipt(t *testing.T) {
	started := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	output := json.RawMessage(`{"valid":true}`)
	zero := int64(0)
	invoke := &fakeInvokeBackend{response: protocol.InvocationReceipt{
		ContractVersion: protocol.ContractVersion, InvocationID: "invocation-1",
		BindingID: "binding-inspection", BindingVersion: "1.0.0", Status: protocol.InvocationStatusSucceeded,
		OutputMediaType: "application/json", Output: output, SafeSummary: "Markdown is valid.", TraceID: "trace-1",
		StartedAt: started, FinishedAt: timePointer(started.Add(time.Millisecond)), DurationMillis: 1,
		InputBytes: 20, OutputBytes: int64(len(output)), CostMinor: &zero, Currency: "CNY",
	}}
	caller := testCaller()
	server, err := New(Config{Caller: caller, Search: &fakeSearchBackend{}, Invoke: invoke})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session := connect(t, server)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if len(listed.Tools) != 2 || !names[string(protocol.ToolSearchCapabilities)] || !names[string(protocol.ToolInvokeCapability)] {
		t.Fatalf("tools = %#v", listed.Tools)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolInvokeCapability),
		Arguments: map[string]any{
			"bindingId": "binding-inspection", "bindingVersion": "1.0.0", "idempotencyKey": "invoke-1",
			"input": map[string]any{"content": "# Ready\n\nBody"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError || result.StructuredContent == nil || invoke.request.Caller != caller || invoke.request.BindingID != "binding-inspection" {
		t.Fatalf("result=%#v request=%#v", result, invoke.request)
	}
}

func TestHandoffToolsInjectHostIdentityAndReturnMissionDerivedStatus(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	reference := protocol.HandoffRef{
		ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", MissionID: "mission-1", Status: protocol.HandoffStatusInProgress,
		ExpectedArtifacts: []protocol.ArtifactExpectation{{LogicalName: "result.txt", MediaType: "text/plain"}},
		Provider:          protocol.ProviderSummary{ProviderID: "provider-1", DisplayName: "Provider One", Kind: "PLATFORM"},
		CreatedAt:         now, DeepLink: "/missions/mission-1", PollAfterMillis: 1000,
	}
	backend := &fakeHandoffBackend{
		reference: reference,
		list: protocol.ListHandoffsResponse{ContractVersion: protocol.ContractVersion, Items: []protocol.HandoffListItem{{
			HandoffRef: reference, SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, Next: protocol.ResumeDirectiveWait,
			SafeSummary: "The handoff is running.", UpdatedAt: now.Add(time.Second),
		}}},
		status: protocol.HandoffStatusView{
			HandoffRef: reference, SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, UpdateCursor: "sha256:" + strings.Repeat("c", 64), Changed: true,
			Resume:      protocol.HandoffResumePayload{Next: protocol.ResumeDirectiveWait},
			SafeSummary: "The handoff is running.", UpdatedAt: now.Add(time.Second),
		},
		artifact: protocol.ArtifactReference{
			ContractVersion: protocol.ContractVersion, ArtifactID: "artifact-1", LogicalName: "result.txt", MediaType: "text/plain",
			ContentHash: "sha256:" + strings.Repeat("a", 64), ContentURI: "/api/v1/missions/mission-1/artifacts/artifact-1/content",
		},
		acceptance: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", Status: protocol.HandoffStatusAccepted,
			MissionVersion: 3, RecordedAt: now.Add(2 * time.Second),
		},
		rejection: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", Status: protocol.HandoffStatusRecovery,
			MissionVersion: 3, RecordedAt: now.Add(2 * time.Second),
		},
	}
	caller := testCaller()
	server, err := New(Config{Caller: caller, Search: &fakeSearchBackend{}, Handoff: backend})
	if err != nil {
		t.Fatal(err)
	}
	session := connect(t, server)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	if len(listed.Tools) != 7 || !names[string(protocol.ToolHandoffWork)] || !names[string(protocol.ToolListHandoffs)] || !names[string(protocol.ToolGetHandoffStatus)] ||
		!names[string(protocol.ToolFetchArtifact)] || !names[string(protocol.ToolAcceptDelivery)] || !names[string(protocol.ToolRejectDelivery)] {
		t.Fatalf("tools=%#v", listed.Tools)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolHandoffWork), Arguments: map[string]any{
			"bindingId": "binding-1", "bindingVersion": "1.0.0", "idempotencyKey": "handoff-key-1",
			"goal": "Produce a verified result", "expectedArtifacts": []map[string]any{{"logicalName": "result.txt", "mediaType": "text/plain"}},
		},
	})
	if err != nil || result.IsError || backend.handoffRequest.Caller != caller {
		t.Fatalf("handoff result=%#v request=%#v err=%v", result, backend.handoffRequest, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolListHandoffs), Arguments: map[string]any{"limit": float64(5)},
	})
	if err != nil || result.IsError || backend.listRequest.Caller != caller || backend.listRequest.Limit != 5 {
		t.Fatalf("list result=%#v request=%#v err=%v", result, backend.listRequest, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolGetHandoffStatus), Arguments: map[string]any{
			"handoffId": "handoff-1", "knownUpdateCursor": "sha256:" + strings.Repeat("b", 64),
		},
	})
	if err != nil || result.IsError || backend.statusRequest.Caller != caller ||
		backend.statusRequest.KnownUpdateCursor != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("status result=%#v request=%#v err=%v", result, backend.statusRequest, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolFetchArtifact), Arguments: map[string]any{"handoffId": "handoff-1", "artifactId": "artifact-1"},
	})
	if err != nil || result.IsError || backend.artifactRequest.Caller != caller {
		t.Fatalf("artifact result=%#v request=%#v err=%v", result, backend.artifactRequest, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolAcceptDelivery), Arguments: map[string]any{
			"handoffId": "handoff-1", "missionVersion": float64(2), "idempotencyKey": "accept-1", "reason": "Verified",
		},
	})
	if err != nil || result.IsError || backend.acceptanceRequest.Caller != caller {
		t.Fatalf("accept result=%#v request=%#v err=%v", result, backend.acceptanceRequest, err)
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolRejectDelivery), Arguments: map[string]any{
			"handoffId": "handoff-1", "missionVersion": float64(2), "idempotencyKey": "reject-1", "reason": "Revise",
		},
	})
	if err != nil || result.IsError || backend.rejectionRequest.Caller != caller {
		t.Fatalf("reject result=%#v request=%#v err=%v", result, backend.rejectionRequest, err)
	}
}

func TestSearchToolReturnsOnlyHostSafeBackendError(t *testing.T) {
	backend := &fakeSearchBackend{err: protocol.NewUserActionError(
		protocol.ErrorCodeAuthorizationDenied,
		"This Host installation cannot use that scope",
		"disclosure-1",
		"https://agentx.example/settings/agent-bridge/context?disclosure=disclosure-1",
	)}
	server, err := New(Config{Caller: testCaller(), Search: backend})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := connect(t, server).CallTool(context.Background(), &mcp.CallToolParams{
		Name: string(protocol.ToolSearchCapabilities), Arguments: map[string]any{"query": "secret"},
	})
	if err != nil {
		t.Fatalf("CallTool protocol error: %v", err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	var safeError protocol.Error
	if !ok || json.Unmarshal([]byte(text.Text), &safeError) != nil ||
		safeError.Code != protocol.ErrorCodeAuthorizationDenied || !safeError.NeedsUser ||
		safeError.ResourceRef != "disclosure-1" || safeError.DeepLink == "" || strings.Contains(text.Text, "stack") {
		t.Fatalf("error content = %#v", result.Content)
	}
}

func connect(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "agentx-test", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func testCaller() protocol.CallerContext {
	return protocol.CallerContext{PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-codex-1", HostKind: protocol.HostKindCodex}
}

type fakeSearchBackend struct {
	request  protocol.SearchCapabilitiesRequest
	response protocol.SearchCapabilitiesResponse
	err      error
}

func (backend *fakeSearchBackend) Search(_ context.Context, request protocol.SearchCapabilitiesRequest) (protocol.SearchCapabilitiesResponse, error) {
	backend.request = request
	return backend.response, backend.err
}

type fakeInvokeBackend struct {
	request  protocol.InvokeCapabilityRequest
	response protocol.InvocationReceipt
	err      error
}

type fakeHandoffBackend struct {
	handoffRequest    protocol.HandoffWorkRequest
	listRequest       protocol.ListHandoffsRequest
	statusRequest     protocol.GetHandoffStatusRequest
	artifactRequest   protocol.FetchArtifactRequest
	acceptanceRequest protocol.DeliveryDecisionRequest
	rejectionRequest  protocol.DeliveryDecisionRequest
	reference         protocol.HandoffRef
	list              protocol.ListHandoffsResponse
	status            protocol.HandoffStatusView
	artifact          protocol.ArtifactReference
	acceptance        protocol.DeliveryDecisionReceipt
	rejection         protocol.DeliveryDecisionReceipt
	err               error
}

func (backend *fakeHandoffBackend) Handoff(_ context.Context, request protocol.HandoffWorkRequest) (protocol.HandoffRef, error) {
	backend.handoffRequest = request
	return backend.reference, backend.err
}

func (backend *fakeHandoffBackend) List(_ context.Context, request protocol.ListHandoffsRequest) (protocol.ListHandoffsResponse, error) {
	backend.listRequest = request
	return backend.list, backend.err
}

func (backend *fakeHandoffBackend) Status(_ context.Context, request protocol.GetHandoffStatusRequest) (protocol.HandoffStatusView, error) {
	backend.statusRequest = request
	return backend.status, nil
}

func (backend *fakeHandoffBackend) FetchArtifact(_ context.Context, request protocol.FetchArtifactRequest) (protocol.ArtifactReference, error) {
	backend.artifactRequest = request
	return backend.artifact, nil
}

func (backend *fakeHandoffBackend) AcceptDelivery(_ context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	backend.acceptanceRequest = request
	return backend.acceptance, nil
}

func (backend *fakeHandoffBackend) RejectDelivery(_ context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	backend.rejectionRequest = request
	return backend.rejection, nil
}

func timePointer(value time.Time) *time.Time { return &value }

func (backend *fakeInvokeBackend) Invoke(_ context.Context, request protocol.InvokeCapabilityRequest) (protocol.InvocationReceipt, error) {
	backend.request = request
	return backend.response, backend.err
}
