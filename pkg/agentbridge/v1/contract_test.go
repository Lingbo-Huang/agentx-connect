package agentbridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolSurfaceIsStableAndMinimal(t *testing.T) {
	want := []ToolName{
		ToolSearchCapabilities,
		ToolInvokeCapability,
		ToolHandoffWork,
		ToolListHandoffs,
		ToolGetHandoffStatus,
		ToolFetchArtifact,
		ToolAcceptDelivery,
		ToolRejectDelivery,
	}
	got := ToolSurface()
	if len(got) != len(want) {
		t.Fatalf("tool count = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tool[%d] = %q, want %q", index, got[index], want[index])
		}
	}
	got[0] = "mutated"
	if ToolSurface()[0] != ToolSearchCapabilities {
		t.Fatal("ToolSurface exposed mutable package state")
	}
}

func TestValidateListHandoffsContractsBoundedOrderedDiscovery(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	request := ListHandoffsRequest{ContractVersion: ContractVersion, Caller: CallerContext{
		PrincipalID: "principal_user_1", SpaceID: "space_private_1", HostInstallationID: "host_workbuddy_1", HostKind: HostKindWorkBuddy,
	}, Limit: MaxHandoffListLimit}
	if err := ValidateListHandoffsRequest(request); err != nil {
		t.Fatalf("valid list request: %v", err)
	}
	request.Limit++
	if err := ValidateListHandoffsRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("oversized list request error=%v", err)
	}
	item := HandoffListItem{HandoffRef: HandoffRef{
		ContractVersion: ContractVersion, HandoffID: "handoff-1", MissionID: "mission-1", Status: HandoffStatusInProgress,
		ExpectedArtifacts: []ArtifactExpectation{{LogicalName: "result.txt", MediaType: "text/plain"}},
		Provider:          ProviderSummary{ProviderID: "provider-1", DisplayName: "Provider One", Kind: "PLATFORM"},
		CreatedAt:         now, DeepLink: "/missions/mission-1", PollAfterMillis: 1000,
	}, SourceHostKind: HostKindWorkBuddy, Goal: "Produce one verified result", MissionVersion: 2, Next: ResumeDirectiveWait, SafeSummary: "The handoff is running.", UpdatedAt: now.Add(time.Minute)}
	response := ListHandoffsResponse{ContractVersion: ContractVersion, Items: []HandoffListItem{item}}
	if err := ValidateListHandoffsResponse(response); err != nil {
		t.Fatalf("valid list response: %v", err)
	}
}

func TestCompactToolSurfaceIsStableAndMinimal(t *testing.T) {
	want := []ToolName{ToolSearchCapabilities, ToolUseCapability}
	got := CompactToolSurface()
	if len(got) != len(want) {
		t.Fatalf("compact tool count = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("compact tool[%d] = %q, want %q", index, got[index], want[index])
		}
	}
	got[0] = "mutated"
	if CompactToolSurface()[0] != ToolSearchCapabilities {
		t.Fatal("CompactToolSurface exposed mutable package state")
	}
}

func TestValidateSearchRequiresBoundedHostIdentityAndQuery(t *testing.T) {
	request := SearchCapabilitiesRequest{
		ContractVersion: ContractVersion,
		Caller: CallerContext{
			PrincipalID: "principal_user_1", SpaceID: "space_private_1",
			HostInstallationID: "host_codex_1", HostKind: HostKindCodex,
		},
		Query:                 "find a verified marketing research service",
		ExpectedArtifactTypes: []string{"text/markdown"},
		MaximumSensitivity:    SensitivityConfidential,
		Mode:                  RoutingModeBalanced,
	}
	if err := ValidateSearchCapabilitiesRequest(request); err != nil {
		t.Fatalf("valid search: %v", err)
	}
	request.Caller.HostInstallationID = ""
	if err := ValidateSearchCapabilitiesRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("missing HostInstallation error = %v", err)
	}
	request.Caller.HostInstallationID = "host_codex_1"
	request.Query = string(make([]byte, MaxQueryBytes+1))
	if err := ValidateSearchCapabilitiesRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("oversized query error = %v", err)
	}
}

func TestWorkBuddyIsAFirstClassHostKind(t *testing.T) {
	caller := CallerContext{
		PrincipalID: "principal_user_1", SpaceID: "space_private_1",
		HostInstallationID: "host_workbuddy_1", HostKind: HostKindWorkBuddy,
	}
	if err := ValidateCallerContext(caller); err != nil {
		t.Fatalf("valid WorkBuddy caller: %v", err)
	}
}

func TestTraeIsAFirstClassHostKind(t *testing.T) {
	caller := CallerContext{
		PrincipalID: "principal_user_1", SpaceID: "space_private_1",
		HostInstallationID: "host_trae_1", HostKind: HostKindTrae,
	}
	if err := ValidateCallerContext(caller); err != nil {
		t.Fatalf("valid Trae caller: %v", err)
	}
}

func TestSealIsAFirstClassHostKind(t *testing.T) {
	caller := CallerContext{
		PrincipalID: "principal_user_1", SpaceID: "space_private_1",
		HostInstallationID: "host_seal_1", HostKind: HostKindSeal,
	}
	if err := ValidateCallerContext(caller); err != nil {
		t.Fatalf("valid Seal/OpenClaw caller: %v", err)
	}
}

func TestValidateCapabilityMatchRequiresExplicitExecutionMode(t *testing.T) {
	match := CapabilityMatch{
		CapabilityID: "capability.marketing.research", ServiceID: "service.marketing", ServiceVersionID: "service.marketing@1.0.0",
		DisplayName: "Marketing research", Description: "Produces an evidence-backed marketing report.", CapabilityCodes: []string{"capability.marketing.research"},
		BindingID: "binding.provider.marketing", BindingVersion: "1.0.0",
		ExecutionMode: ExecutionModeHandoff, Provider: ProviderSummary{ProviderID: "provider.opc", DisplayName: "OPC Studio", Kind: "THIRD_PARTY"},
		ArtifactTypes: []string{"text/markdown"}, VerifierKinds: []string{"citation.check"},
		ConnectionState: ConnectionStateReady, GrantState: GrantStateGranted,
		EstimatedDurationMillis: 90_000,
	}
	if err := ValidateCapabilityMatch(match); err != nil {
		t.Fatalf("valid match: %v", err)
	}
	match.ExecutionMode = ""
	if err := ValidateCapabilityMatch(match); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("missing execution mode error = %v", err)
	}
	match.ExecutionMode = ExecutionMode("AUTO")
	if err := ValidateCapabilityMatch(match); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("invented execution mode error = %v", err)
	}
}

func TestSearchResponseRejectsDuplicateBindingVersion(t *testing.T) {
	match := CapabilityMatch{
		CapabilityID: "capability.marketing.research", ServiceID: "service.marketing", ServiceVersionID: "service.marketing@1.0.0",
		DisplayName: "Marketing research", Description: "Produces an evidence-backed marketing report.", CapabilityCodes: []string{"capability.marketing.research"},
		BindingID: "binding.provider.marketing", BindingVersion: "1.0.0", ExecutionMode: ExecutionModeHandoff,
		Provider:      ProviderSummary{ProviderID: "provider.opc", DisplayName: "OPC Studio", Kind: "THIRD_PARTY"},
		ArtifactTypes: []string{"text/markdown"}, VerifierKinds: []string{"citation.check"},
		ConnectionState: ConnectionStateReady, GrantState: GrantStateGranted,
	}
	response := SearchCapabilitiesResponse{
		ContractVersion: ContractVersion, Disposition: SearchDispositionMatchesFound,
		NextAction: SearchNextActionSelectExactMatch, SafeSummary: "Two matches were found.",
		Matches: []CapabilityMatch{match, match},
	}
	if err := ValidateSearchCapabilitiesResponse(response); err == nil {
		t.Fatal("ValidateSearchCapabilitiesResponse accepted duplicate Binding version")
	}
}

func TestSearchResponseRequiresExplicitGapAndForbidsAutomaticFallback(t *testing.T) {
	gap := SearchCapabilitiesResponse{
		ContractVersion: ContractVersion, Disposition: SearchDispositionCapabilityGap,
		NextAction: SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
	}
	if err := ValidateSearchCapabilitiesResponse(gap); err != nil {
		t.Fatalf("valid capability gap: %v", err)
	}
	gap.AutomaticFallbackAllowed = true
	if err := ValidateSearchCapabilitiesResponse(gap); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("automatic fallback error = %v", err)
	}
	gap.AutomaticFallbackAllowed = false
	gap.NextAction = SearchNextActionSelectExactMatch
	if err := ValidateSearchCapabilitiesResponse(gap); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("inconsistent gap action error = %v", err)
	}
}

func TestCapabilityMatchUsesExplicitMillisecondsOnTheWire(t *testing.T) {
	encoded, err := json.Marshal(CapabilityMatch{EstimatedDurationMillis: 90_000})
	if err != nil {
		t.Fatalf("marshal CapabilityMatch: %v", err)
	}
	if !strings.Contains(string(encoded), `"estimatedDurationMillis":90000`) || strings.Contains(string(encoded), `"estimatedDuration":`) {
		t.Fatalf("duration wire shape = %s", encoded)
	}
}

func TestValidateInvokeRequiresBoundedJSONAndIdempotency(t *testing.T) {
	request := InvokeCapabilityRequest{
		ContractVersion: ContractVersion, Caller: validCaller(),
		BindingID: "binding.agentx.markdown-inspection", BindingVersion: "1.0.0",
		IdempotencyKey: "inspect-release-note-1", Input: json.RawMessage(`{"content":"# Release\n\nReady"}`),
	}
	if err := ValidateInvokeCapabilityRequest(request); err != nil {
		t.Fatalf("valid invoke: %v", err)
	}
	request.Input = json.RawMessage(`{"content":`)
	if err := ValidateInvokeCapabilityRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("malformed JSON error = %v", err)
	}
	request.Input = json.RawMessage(`null`)
	if err := ValidateInvokeCapabilityRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("null input error = %v", err)
	}
	request.Input = json.RawMessage(`{"content":"# Release\n\nReady"}`)
	request.IdempotencyKey = ""
	if err := ValidateInvokeCapabilityRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("missing idempotency error = %v", err)
	}
}

func TestValidateHandoffRequiresBoundedArtifactsAndContextReferences(t *testing.T) {
	request := HandoffWorkRequest{
		ContractVersion: ContractVersion, Caller: validCaller(),
		BindingID: "service-binding.foundation.fake-success", BindingVersion: "1.0.0",
		IdempotencyKey: "handoff-release-note-1", Goal: "Produce one verified release note",
		ExpectedArtifacts: []ArtifactExpectation{{LogicalName: "release.md", MediaType: "text/markdown"}},
	}
	if err := ValidateHandoffWorkRequest(request); err != nil {
		t.Fatalf("valid handoff request: %v", err)
	}
	request.Context = []ContextReference{{
		Name: "public brief", URI: "https://example.com/brief.md", MediaType: "text/markdown", SizeBytes: 42,
		ContentHash: "sha256:" + strings.Repeat("a", 64), Sensitivity: SensitivityPublic,
	}}
	if err := ValidateHandoffWorkRequest(request); err != nil {
		t.Fatalf("valid Context reference: %v", err)
	}
	request.Context[0].URI = "agentx-input://local/input_0123456789abcdef0123456789abcdef"
	if err := ValidateHandoffWorkRequest(request); err != nil {
		t.Fatalf("valid AgentX-controlled input reference: %v", err)
	}
	request.Context[0].URI = "https://example.com/brief.md"
	request.Context[0].ContentHash = "sha256:not-a-version"
	if err := ValidateHandoffWorkRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("mutable Context reference error = %v", err)
	}
	request.Context[0].ContentHash = "sha256:" + strings.Repeat("a", 64)
	request.Context[0].URI = "file:///tmp/brief.md"
	if err := ValidateHandoffWorkRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("unsafe Context URI error = %v", err)
	}
}

func TestValidateHandoffStatusAndDecisionContracts(t *testing.T) {
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	reference := HandoffRef{
		ContractVersion: ContractVersion, HandoffID: "handoff_1", MissionID: "mission_1",
		Status: HandoffStatusQueued, ExpectedArtifacts: []ArtifactExpectation{{LogicalName: "result.txt", MediaType: "text/plain"}},
		Provider:  ProviderSummary{ProviderID: "provider.agentx", DisplayName: "AgentX", Kind: "AGENTX_OFFICIAL"},
		CreatedAt: now, DeepLink: "/missions/mission_1", PollAfterMillis: 1_000,
	}
	if err := ValidateHandoffRef(reference); err != nil {
		t.Fatalf("valid HandoffRef: %v", err)
	}
	view := HandoffStatusView{
		HandoffRef: reference, SourceHostKind: HostKindCodex, Goal: "Produce one verified result", MissionVersion: 1, UpdateCursor: "sha256:" + strings.Repeat("c", 64), Changed: true,
		Resume:      HandoffResumePayload{Next: ResumeDirectiveWait},
		SafeSummary: "Work is queued.", UpdatedAt: now,
	}
	if err := ValidateHandoffStatusView(view); err != nil {
		t.Fatalf("valid status view: %v", err)
	}
	request := GetHandoffStatusRequest{ContractVersion: ContractVersion, Caller: validCaller(), HandoffID: reference.HandoffID, KnownUpdateCursor: view.UpdateCursor}
	if err := ValidateGetHandoffStatusRequest(request); err != nil {
		t.Fatalf("valid known update cursor: %v", err)
	}
	request.KnownUpdateCursor = "mission-version:1"
	if err := ValidateGetHandoffStatusRequest(request); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("unsafe update cursor error = %v", err)
	}
	view.Resume.Next = ResumeDirectiveReviewDelivery
	if err := ValidateHandoffStatusView(view); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("status-mismatched resume error = %v", err)
	}
	decision := DeliveryDecisionRequest{
		ContractVersion: ContractVersion, Caller: validCaller(), HandoffID: "handoff_1",
		MissionVersion: 5, IdempotencyKey: "accept-handoff-1", Reason: "The result is usable.",
	}
	if err := ValidateDeliveryDecisionRequest(decision); err != nil {
		t.Fatalf("valid delivery decision: %v", err)
	}
	decision.MissionVersion = 0
	if err := ValidateDeliveryDecisionRequest(decision); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("zero-version decision error = %v", err)
	}
}

func TestValidateInvocationReceiptSeparatesUnknownFromTerminalFacts(t *testing.T) {
	started := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	receipt := InvocationReceipt{
		ContractVersion: ContractVersion, InvocationID: "invocation_1",
		BindingID: "binding.agentx.markdown-inspection", BindingVersion: "1.0.0",
		Status: InvocationStatusUnknown, SafeSummary: "Invocation was reserved.",
		TraceID: "trace_1", StartedAt: started, InputBytes: 32,
	}
	if err := ValidateInvocationReceipt(receipt); err != nil {
		t.Fatalf("valid unknown receipt: %v", err)
	}
	unknownWire, err := json.Marshal(receipt)
	if err != nil || strings.Contains(string(unknownWire), `"finishedAt"`) {
		t.Fatalf("UNKNOWN receipt exposed a fake finish time: %s err=%v", unknownWire, err)
	}
	receipt.Status = InvocationStatusSucceeded
	if err := ValidateInvocationReceipt(receipt); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("terminal receipt without finish error = %v", err)
	}
	finished := started.Add(25 * time.Millisecond)
	receipt.FinishedAt = &finished
	receipt.DurationMillis = 25
	receipt.OutputMediaType = "application/vnd.agentx.markdown-inspection+json"
	receipt.Output = json.RawMessage(`{"valid":true}`)
	receipt.OutputBytes = int64(len(receipt.Output))
	zero := int64(0)
	receipt.CostMinor = &zero
	receipt.Currency = "CNY"
	if err := ValidateInvocationReceipt(receipt); err != nil {
		t.Fatalf("valid terminal receipt: %v", err)
	}
	receipt.Output = json.RawMessage(`{"valid":`)
	if err := ValidateInvocationReceipt(receipt); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("malformed output error = %v", err)
	}
}

func validCaller() CallerContext {
	return CallerContext{PrincipalID: "principal_user_1", SpaceID: "space_private_1", HostInstallationID: "host_codex_1", HostKind: HostKindCodex}
}

func TestValidateHandoffRefRequiresDurableMissionMapping(t *testing.T) {
	ref := HandoffRef{
		ContractVersion: ContractVersion, HandoffID: "handoff_1", MissionID: "mission_1",
		Status: HandoffStatusQueued, ExpectedArtifacts: []ArtifactExpectation{{LogicalName: "report.md", MediaType: "text/markdown"}},
		Provider:  ProviderSummary{ProviderID: "provider_agentx", DisplayName: "AgentX", Kind: "PLATFORM"},
		CreatedAt: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC), DeepLink: "/missions/mission_1",
		PortalURL: "/handoffs/handoff_1", PollAfterMillis: 2_000,
	}
	if err := ValidateHandoffRef(ref); err != nil {
		t.Fatalf("valid HandoffRef: %v", err)
	}
	if ref.PortalURL != "/handoffs/"+ref.HandoffID || ref.DeepLink == "" {
		t.Fatalf("additive portalUrl/deepLink contract = %#v", ref)
	}
	ref.MissionID = ""
	if err := ValidateHandoffRef(ref); !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("missing Mission mapping error = %v", err)
	}
}

func TestStableErrorTellsHostWhetherItCanRetryOrNeedsUser(t *testing.T) {
	err := NewUserActionError(ErrorCodeConnectionRequired, "Connect the provider account", "handoff_1", "/settings/connections/handoff_1")
	var bridgeError *Error
	if !AsError(err, &bridgeError) {
		t.Fatalf("error type = %T", err)
	}
	if bridgeError.Retryable || !bridgeError.NeedsUser || bridgeError.ResourceRef != "handoff_1" || bridgeError.DeepLink != "/settings/connections/handoff_1" {
		t.Fatalf("error metadata = %#v", bridgeError)
	}
	var wire map[string]any
	if json.Unmarshal([]byte(err.Error()), &wire) != nil || wire["code"] != string(ErrorCodeConnectionRequired) ||
		wire["userActionRequired"] != true || wire["deepLink"] != "/settings/connections/handoff_1" {
		t.Fatalf("error text must preserve Host-safe structured metadata: %q", err.Error())
	}
}
