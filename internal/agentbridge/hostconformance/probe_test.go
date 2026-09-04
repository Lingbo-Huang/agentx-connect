package hostconformance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostauth"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostconformance"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/mcpserver"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

func TestProbeAcceptsFullAndCompactAgentBridgeProfiles(t *testing.T) {
	for _, profile := range []mcpserver.ToolProfile{mcpserver.ToolProfileFull, mcpserver.ToolProfileCompact} {
		t.Run(string(profile), func(t *testing.T) {
			server, err := mcpserver.New(mcpserver.Config{
				Caller: testCaller(), Search: searchBackend{}, Invoke: invokeBackend{}, Handoff: handoffBackend{}, Profile: profile,
			})
			if err != nil {
				t.Fatal(err)
			}
			clientTransport, closeServer := serve(t, server)
			defer closeServer()
			evidence, err := hostconformance.Probe(context.Background(), clientTransport, profile)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			want := len(protocol.ToolSurface())
			if profile == mcpserver.ToolProfileCompact {
				want = len(protocol.CompactToolSurface())
			}
			if evidence.ToolCount != want || evidence.MatchCount != 0 {
				t.Fatalf("evidence=%#v wantTools=%d", evidence, want)
			}
		})
	}
}

func TestProbeRejectsTheWrongExpectedProfile(t *testing.T) {
	server, err := mcpserver.New(mcpserver.Config{
		Caller: testCaller(), Search: searchBackend{}, Invoke: invokeBackend{}, Handoff: handoffBackend{}, Profile: mcpserver.ToolProfileCompact,
	})
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, closeServer := serve(t, server)
	defer closeServer()
	if _, err := hostconformance.Probe(context.Background(), clientTransport, mcpserver.ToolProfileFull); err == nil {
		t.Fatal("Probe accepted a compact server as the full profile")
	}
}

func TestProbeCommandRequiresAbsolutePrivatePaths(t *testing.T) {
	_, err := hostconformance.ProbeCommand(context.Background(), hostconformance.CommandSpec{
		Executable: "agentx-bridge-mcp", CredentialFile: filepath.Join(t.TempDir(), "host.json"),
		Profile: mcpserver.ToolProfileFull,
	})
	if err == nil {
		t.Fatal("ProbeCommand accepted a relative executable")
	}
}

func TestProbeCommandStartsTheRealAgentBridgeBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer process-probe-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion,
			Disposition:     protocol.SearchDispositionCapabilityGap,
			NextAction:      protocol.SearchNextActionAskUserOrReportGap,
			SafeSummary:     "No eligible capability matched the conformance diagnostic.",
		})
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	executable := filepath.Join(directory, "agentx-bridge-mcp")
	build := exec.Command("go", "build", "-o", executable, "../../../cmd/agentx-bridge-mcp")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real Agent Bridge: %v\n%s", err, output)
	}
	credentialFile := filepath.Join(directory, "host.json")
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialFile, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "process-probe-secret", ExpiresAt: &expiresAt,
		Caller: testCaller(),
	}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	evidence, err := hostconformance.ProbeCommand(ctx, hostconformance.CommandSpec{
		Executable: executable, CredentialFile: credentialFile, Profile: mcpserver.ToolProfileCompact,
	})
	if err != nil {
		t.Fatalf("ProbeCommand: %v", err)
	}
	if evidence.Profile != mcpserver.ToolProfileCompact || evidence.ToolCount != len(protocol.CompactToolSurface()) {
		t.Fatalf("evidence=%#v", evidence)
	}
}

func serve(t *testing.T, server *mcp.Server) (*mcp.InMemoryTransport, func()) {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	session, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return clientTransport, func() { _ = session.Close() }
}

func testCaller() protocol.CallerContext {
	return protocol.CallerContext{PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex}
}

type searchBackend struct{}

func (searchBackend) Search(context.Context, protocol.SearchCapabilitiesRequest) (protocol.SearchCapabilitiesResponse, error) {
	return protocol.SearchCapabilitiesResponse{
		ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
		NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability matched the diagnostic query.",
	}, nil
}

type invokeBackend struct{}

func (invokeBackend) Invoke(context.Context, protocol.InvokeCapabilityRequest) (protocol.InvocationReceipt, error) {
	panic("conformance must not invoke a capability")
}

type handoffBackend struct{}

func (handoffBackend) Handoff(context.Context, protocol.HandoffWorkRequest) (protocol.HandoffRef, error) {
	panic("conformance must not create a handoff")
}
func (handoffBackend) List(context.Context, protocol.ListHandoffsRequest) (protocol.ListHandoffsResponse, error) {
	panic("conformance must not list business data")
}
func (handoffBackend) Status(context.Context, protocol.GetHandoffStatusRequest) (protocol.HandoffStatusView, error) {
	panic("conformance must not read a specific handoff")
}
func (handoffBackend) FetchArtifact(context.Context, protocol.FetchArtifactRequest) (protocol.ArtifactReference, error) {
	panic("conformance must not fetch an artifact")
}
func (handoffBackend) AcceptDelivery(context.Context, protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	panic("conformance must not accept delivery")
}
func (handoffBackend) RejectDelivery(context.Context, protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	panic("conformance must not reject delivery")
}
