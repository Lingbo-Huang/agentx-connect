package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

func TestClientAndHandlerInjectAuthenticatedCaller(t *testing.T) {
	caller := testCaller()
	backend := &recordingSearch{response: validResponse()}
	handler, err := NewHandler(HandlerConfig{Search: backend, Authenticate: staticAuthenticator{credential: "secret-host-token", caller: caller}})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(ClientConfig{ServerURL: server.URL, Credential: "secret-host-token"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	request := validRequest()
	request.Caller = protocol.CallerContext{
		PrincipalID: "untrusted-local-value", SpaceID: "untrusted-space", HostInstallationID: "untrusted-host", HostKind: protocol.HostKindOther,
	}
	response, err := client.Search(context.Background(), request)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(response.Matches) != 1 || backend.request.Caller != caller {
		t.Fatalf("response=%#v request=%#v", response, backend.request)
	}
	if backend.request.Query != request.Query || backend.request.ContractVersion != protocol.ContractVersion {
		t.Fatalf("backend request=%#v", backend.request)
	}
}

func TestInvokeClientAndHandlerInjectAuthenticatedCaller(t *testing.T) {
	caller := testCaller()
	started := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	output := json.RawMessage(`{"valid":true}`)
	zero := int64(0)
	backend := &recordingInvoke{response: protocol.InvocationReceipt{
		ContractVersion: protocol.ContractVersion, InvocationID: "invocation-1",
		BindingID: "binding-inspection", BindingVersion: "1.0.0", Status: protocol.InvocationStatusSucceeded,
		OutputMediaType: "application/json", Output: output, SafeSummary: "Markdown is valid.",
		TraceID: "trace-1", StartedAt: started, FinishedAt: timePointer(started.Add(time.Millisecond)), DurationMillis: 1,
		InputBytes: 20, OutputBytes: int64(len(output)), CostMinor: &zero, Currency: "CNY",
	}}
	handler, err := NewHandler(HandlerConfig{
		Search: &recordingSearch{response: validResponse()}, Invoke: backend,
		Authenticate: staticAuthenticator{credential: "secret-host-token", caller: caller},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(ClientConfig{ServerURL: server.URL, Credential: "secret-host-token"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	request := protocol.InvokeCapabilityRequest{
		ContractVersion: protocol.ContractVersion,
		Caller:          protocol.CallerContext{PrincipalID: "attacker", SpaceID: "other", HostInstallationID: "other", HostKind: protocol.HostKindOther},
		BindingID:       "binding-inspection", BindingVersion: "1.0.0", IdempotencyKey: "invoke-1",
		Input: json.RawMessage(`{"content":"# Ready\n\nBody"}`),
	}
	response, err := client.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if response.InvocationID != "invocation-1" || backend.request.Caller != caller || backend.request.BindingID != request.BindingID {
		t.Fatalf("response=%#v request=%#v", response, backend.request)
	}
}

func TestHandoffClientAndHandlerInjectAuthenticatedCallerAndExposeStatus(t *testing.T) {
	caller := testCaller()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	backend := &recordingHandoff{
		reference: validHandoffRef(now),
		list: protocol.ListHandoffsResponse{ContractVersion: protocol.ContractVersion, Items: []protocol.HandoffListItem{{
			HandoffRef: validHandoffRef(now), SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, Next: protocol.ResumeDirectiveReviewDelivery,
			SafeSummary: "The delivery is ready for acceptance.", UpdatedAt: now.Add(time.Minute),
		}}},
		status: protocol.HandoffStatusView{
			HandoffRef: validHandoffRef(now), SourceHostKind: protocol.HostKindCodex, Goal: "Produce one verified result", MissionVersion: 2, UpdateCursor: "sha256:" + strings.Repeat("c", 64), Changed: true,
			ArtifactRefs: []string{"artifact-1"},
			Resume: protocol.HandoffResumePayload{
				Next: protocol.ResumeDirectiveReviewDelivery,
				Artifacts: []protocol.HandoffArtifactSummary{{
					ArtifactID: "artifact-1", LogicalName: "release.md", MediaType: "text/markdown",
					ContentHash: "sha256:" + strings.Repeat("a", 64), SizeBytes: 10,
				}},
			},
			SafeSummary: "The delivery is ready for acceptance.", UpdatedAt: now.Add(time.Minute),
		},
		artifact: protocol.ArtifactReference{
			ContractVersion: protocol.ContractVersion, ArtifactID: "artifact-1", LogicalName: "release.md",
			MediaType: "text/markdown", ContentHash: "sha256:" + strings.Repeat("a", 64),
			ContentURI: "/api/v1/missions/mission-1/artifacts/artifact-1/content",
		},
		acceptance: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", Status: protocol.HandoffStatusAccepted,
			MissionVersion: 3, RecordedAt: now.Add(2 * time.Minute),
		},
		rejection: protocol.DeliveryDecisionReceipt{
			ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", Status: protocol.HandoffStatusRecovery,
			MissionVersion: 3, RecordedAt: now.Add(2 * time.Minute),
		},
	}
	handler, err := NewHandler(HandlerConfig{
		Search: &recordingSearch{response: validResponse()}, Handoff: backend,
		Authenticate: staticAuthenticator{credential: "secret-host-token", caller: caller},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(ClientConfig{ServerURL: server.URL, Credential: "secret-host-token"})
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.HandoffWorkRequest{
		ContractVersion: protocol.ContractVersion,
		Caller:          protocol.CallerContext{PrincipalID: "attacker", SpaceID: "other", HostInstallationID: "other", HostKind: protocol.HostKindOther},
		BindingID:       "binding-release", BindingVersion: "1.0.0", IdempotencyKey: "handoff-1",
		Goal: "Deliver a verified release", ExpectedArtifacts: []protocol.ArtifactExpectation{{LogicalName: "release.md", MediaType: "text/markdown"}},
	}
	reference, err := client.Handoff(context.Background(), request)
	if err != nil || reference.HandoffID != "handoff-1" || backend.handoffRequest.Caller != caller {
		t.Fatalf("Handoff() reference=%#v request=%#v err=%v", reference, backend.handoffRequest, err)
	}
	if reference.DeepLink != server.URL+"/missions/mission-1" {
		t.Fatalf("Handoff() deepLink=%q, want absolute same-origin URL", reference.DeepLink)
	}
	listed, err := client.List(context.Background(), protocol.ListHandoffsRequest{
		ContractVersion: protocol.ContractVersion, Caller: request.Caller, Limit: 5,
	})
	if err != nil || len(listed.Items) != 1 || backend.listRequest.Caller != caller || backend.listRequest.Limit != 5 {
		t.Fatalf("List()=%#v request=%#v err=%v", listed, backend.listRequest, err)
	}
	if listed.Items[0].DeepLink != server.URL+"/missions/mission-1" {
		t.Fatalf("List() deepLink=%q, want absolute same-origin URL", listed.Items[0].DeepLink)
	}
	view, err := client.Status(context.Background(), protocol.GetHandoffStatusRequest{
		ContractVersion: protocol.ContractVersion, Caller: request.Caller, HandoffID: reference.HandoffID,
		KnownUpdateCursor: "sha256:" + strings.Repeat("b", 64),
	})
	if err != nil || view.Status != protocol.HandoffStatusDelivered || backend.statusRequest.Caller != caller ||
		backend.statusRequest.KnownUpdateCursor != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("Status() view=%#v request=%#v err=%v", view, backend.statusRequest, err)
	}
	if view.DeepLink != server.URL+"/missions/mission-1" {
		t.Fatalf("Status() deepLink=%q, want absolute same-origin URL", view.DeepLink)
	}
	artifact, err := client.FetchArtifact(context.Background(), protocol.FetchArtifactRequest{
		ContractVersion: protocol.ContractVersion, Caller: request.Caller,
		HandoffID: reference.HandoffID, ArtifactID: "artifact-1",
	})
	if err != nil || artifact.ArtifactID != "artifact-1" || backend.artifactRequest.Caller != caller {
		t.Fatalf("FetchArtifact() artifact=%#v request=%#v err=%v", artifact, backend.artifactRequest, err)
	}
	if artifact.ContentURI != server.URL+"/api/v1/missions/mission-1/artifacts/artifact-1/content" {
		t.Fatalf("FetchArtifact() contentUri=%q, want absolute same-origin URL", artifact.ContentURI)
	}
	decision := protocol.DeliveryDecisionRequest{
		ContractVersion: protocol.ContractVersion, Caller: request.Caller, HandoffID: reference.HandoffID,
		MissionVersion: view.MissionVersion, IdempotencyKey: "accept-handoff-1", Reason: "Verified",
	}
	receipt, err := client.AcceptDelivery(context.Background(), decision)
	if err != nil || receipt.Status != protocol.HandoffStatusAccepted || backend.acceptanceRequest.Caller != caller {
		t.Fatalf("AcceptDelivery() receipt=%#v request=%#v err=%v", receipt, backend.acceptanceRequest, err)
	}
	decision.IdempotencyKey = "reject-handoff-1"
	receipt, err = client.RejectDelivery(context.Background(), decision)
	if err != nil || receipt.Status != protocol.HandoffStatusRecovery || backend.rejectionRequest.Caller != caller {
		t.Fatalf("RejectDelivery() receipt=%#v request=%#v err=%v", receipt, backend.rejectionRequest, err)
	}
}

func TestClientReferenceProjectionPreservesAbsoluteHTTPSAndNeverEmbedsCredential(t *testing.T) {
	client, err := NewClient(ClientConfig{ServerURL: "https://agentx.example", Credential: "must-not-leak"})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.resolveSameOriginReference("/missions/mission-1"); got != "https://agentx.example/missions/mission-1" {
		t.Fatalf("relative projection = %q", got)
	}
	external := "https://artifacts.example/result?signature=bounded"
	if got := client.resolveSameOriginReference(external); got != external {
		t.Fatalf("absolute projection = %q", got)
	}
	if strings.Contains(client.resolveSameOriginReference("/missions/mission-1"), "must-not-leak") {
		t.Fatal("Host credential leaked into projected URI")
	}
}

func TestHandlerRejectsCredentialAndUnknownIdentityField(t *testing.T) {
	backend := &recordingSearch{response: validResponse()}
	handler, err := NewHandler(HandlerConfig{Search: backend, Authenticate: staticAuthenticator{credential: "expected", caller: testCaller()}})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	for _, test := range []struct {
		name   string
		token  string
		body   string
		status int
	}{
		{name: "missing bearer", body: `{"query":"release","maximumSensitivity":"INTERNAL","mode":"BALANCED"}`, status: http.StatusUnauthorized},
		{name: "wrong bearer", token: "wrong", body: `{"query":"release","maximumSensitivity":"INTERNAL","mode":"BALANCED"}`, status: http.StatusUnauthorized},
		{name: "caller cannot be supplied", token: "expected", body: `{"query":"release","caller":{"principalId":"attacker"},"maximumSensitivity":"INTERNAL","mode":"BALANCED"}`, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, SearchPath, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var bridgeErr protocol.Error
			if err := json.Unmarshal(response.Body.Bytes(), &bridgeErr); err != nil || bridgeErr.Code == "" {
				t.Fatalf("error body=%q err=%v", response.Body.String(), err)
			}
		})
	}
	if backend.calls != 0 {
		t.Fatalf("backend calls=%d", backend.calls)
	}
}

func TestInvokeHandlerRejectsModelSuppliedCaller(t *testing.T) {
	backend := &recordingInvoke{}
	handler, err := NewHandler(HandlerConfig{
		Search: &recordingSearch{response: validResponse()}, Invoke: backend,
		Authenticate: staticAuthenticator{credential: "expected", caller: testCaller()},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, InvokePath, strings.NewReader(`{
		"bindingId":"binding-inspection","bindingVersion":"1.0.0","idempotencyKey":"invoke-1",
		"input":{"content":"# Ready\\n\\nBody"},"caller":{"principalId":"attacker"}
	}`))
	request.Header.Set("Authorization", "Bearer expected")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || backend.calls != 0 {
		t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), backend.calls)
	}
}

func TestClientDoesNotFollowRedirectWithHostCredential(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirected = true
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	client, err := NewClient(ClientConfig{ServerURL: source.URL, Credential: "must-not-leak"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Search(context.Background(), validRequest()); err == nil {
		t.Fatal("Search followed or accepted redirect")
	}
	if redirected {
		t.Fatal("Host credential request followed a redirect")
	}
}

func TestClientRejectsInsecureNonLoopbackServer(t *testing.T) {
	if _, err := NewClient(ClientConfig{ServerURL: "http://agentx.example", Credential: "secret"}); err == nil {
		t.Fatal("NewClient accepted non-loopback HTTP")
	}
	if _, err := NewClient(ClientConfig{ServerURL: "https://user:password@agentx.example", Credential: "secret"}); err == nil {
		t.Fatal("NewClient accepted URL userinfo")
	}
}

type recordingSearch struct {
	request  protocol.SearchCapabilitiesRequest
	response protocol.SearchCapabilitiesResponse
	err      error
	calls    int
}

type recordingInvoke struct {
	request  protocol.InvokeCapabilityRequest
	response protocol.InvocationReceipt
	err      error
	calls    int
}

type recordingHandoff struct {
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

func (backend *recordingHandoff) Handoff(_ context.Context, request protocol.HandoffWorkRequest) (protocol.HandoffRef, error) {
	backend.handoffRequest = request
	return backend.reference, backend.err
}

func (backend *recordingHandoff) List(_ context.Context, request protocol.ListHandoffsRequest) (protocol.ListHandoffsResponse, error) {
	backend.listRequest = request
	return backend.list, backend.err
}

func TestHandoffClientPreservesAbsoluteUserActionLink(t *testing.T) {
	backend := &recordingHandoff{err: protocol.NewUserActionError(
		protocol.ErrorCodePolicyDenied,
		"Review the exact Context disclosure before this handoff can start",
		"disclosure-1",
		"/settings/agent-bridge/context?disclosure=disclosure-1",
	)}
	handler, err := NewHandler(HandlerConfig{
		Search: &recordingSearch{response: validResponse()}, Handoff: backend,
		Authenticate: staticAuthenticator{credential: "secret-host-token", caller: testCaller()},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(ClientConfig{ServerURL: server.URL, Credential: "secret-host-token"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Handoff(context.Background(), protocol.HandoffWorkRequest{
		ContractVersion: protocol.ContractVersion, Caller: testCaller(),
		BindingID: "binding-release", BindingVersion: "1.0.0", IdempotencyKey: "handoff-disclosure-1",
		Goal:              "Deliver a verified release",
		ExpectedArtifacts: []protocol.ArtifactExpectation{{LogicalName: "release.md", MediaType: "text/markdown"}},
	})
	var bridgeErr *protocol.Error
	if !protocol.AsError(err, &bridgeErr) || !bridgeErr.NeedsUser || bridgeErr.ResourceRef != "disclosure-1" ||
		bridgeErr.DeepLink != server.URL+"/settings/agent-bridge/context?disclosure=disclosure-1" {
		t.Fatalf("Handoff() error=%#v", bridgeErr)
	}
}

func (backend *recordingHandoff) Status(_ context.Context, request protocol.GetHandoffStatusRequest) (protocol.HandoffStatusView, error) {
	backend.statusRequest = request
	return backend.status, nil
}

func (backend *recordingHandoff) FetchArtifact(_ context.Context, request protocol.FetchArtifactRequest) (protocol.ArtifactReference, error) {
	backend.artifactRequest = request
	return backend.artifact, nil
}

func (backend *recordingHandoff) AcceptDelivery(_ context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	backend.acceptanceRequest = request
	return backend.acceptance, nil
}

func (backend *recordingHandoff) RejectDelivery(_ context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	backend.rejectionRequest = request
	return backend.rejection, nil
}

func (invoke *recordingInvoke) Invoke(_ context.Context, request protocol.InvokeCapabilityRequest) (protocol.InvocationReceipt, error) {
	invoke.calls++
	invoke.request = request
	return invoke.response, invoke.err
}

func (search *recordingSearch) Search(_ context.Context, request protocol.SearchCapabilitiesRequest) (protocol.SearchCapabilitiesResponse, error) {
	search.calls++
	search.request = request
	return search.response, search.err
}

type staticAuthenticator struct {
	credential string
	caller     protocol.CallerContext
}

func (auth staticAuthenticator) AuthenticateHost(_ context.Context, credential string) (protocol.CallerContext, error) {
	if credential != auth.credential {
		return protocol.CallerContext{}, protocol.NewError(protocol.ErrorCodeAuthenticationRequired, "Host authentication is invalid or expired", false, true, "")
	}
	return auth.caller, nil
}

func validRequest() protocol.SearchCapabilitiesRequest {
	return protocol.SearchCapabilitiesRequest{
		ContractVersion: protocol.ContractVersion, Caller: testCaller(), Query: "verified release",
		MaximumSensitivity: protocol.SensitivityInternal, Mode: protocol.RoutingModeBalanced,
	}
}

func testCaller() protocol.CallerContext {
	return protocol.CallerContext{PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-codex-1", HostKind: protocol.HostKindCodex}
}

func validResponse() protocol.SearchCapabilitiesResponse {
	return protocol.SearchCapabilitiesResponse{
		ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionMatchesFound,
		NextAction: protocol.SearchNextActionSelectExactMatch, SafeSummary: "One eligible capability was found.",
		Matches: []protocol.CapabilityMatch{{
			CapabilityID: "release.verified", ServiceID: "service-release", ServiceVersionID: "service-release@1",
			DisplayName: "Verified release", Description: "Produces a verified release.", CapabilityCodes: []string{"release.verified"},
			BindingID: "binding-release", BindingVersion: "1.0.0", ExecutionMode: protocol.ExecutionModeHandoff,
			Provider:      protocol.ProviderSummary{ProviderID: "provider.agentx", DisplayName: "AgentX", Kind: "AGENTX_OFFICIAL"},
			ArtifactTypes: []string{"text/markdown"}, VerifierKinds: []string{"release.check"},
			ConnectionState: protocol.ConnectionStateReady, GrantState: protocol.GrantStateGranted,
		}},
	}
}

func validHandoffRef(now time.Time) protocol.HandoffRef {
	return protocol.HandoffRef{
		ContractVersion: protocol.ContractVersion, HandoffID: "handoff-1", MissionID: "mission-1",
		Status:            protocol.HandoffStatusDelivered,
		ExpectedArtifacts: []protocol.ArtifactExpectation{{LogicalName: "release.md", MediaType: "text/markdown"}},
		Provider:          protocol.ProviderSummary{ProviderID: "provider.agentx", DisplayName: "AgentX", Kind: "AGENTX_OFFICIAL"},
		CreatedAt:         now, DeepLink: "/missions/mission-1",
	}
}

func timePointer(value time.Time) *time.Time { return &value }
