package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostauth"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostconformance"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostinstall"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/mcpserver"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

func TestRunRejectsUnknownHostManagementTarget(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"check", "unknown"}, &output, &output); err == nil {
		t.Fatal("run accepted an unknown Host")
	}
}

func TestRunMCPRejectsUnknownHostBeforeCredentialLoad(t *testing.T) {
	var output bytes.Buffer
	err := runMCP([]string{"--host", "unknown"}, &output)
	if err == nil || !strings.Contains(err.Error(), "supported Hosts") {
		t.Fatalf("runMCP error=%v, want unsupported Host", err)
	}
}

func TestHostTargetsAndCredentialsRemainIndependent(t *testing.T) {
	wants := map[string]struct {
		kind protocol.HostKind
		file string
	}{
		"codex":     {protocol.HostKindCodex, "host-installation.json"},
		"claude":    {protocol.HostKindClaude, "host-installation-claude.json"},
		"cursor":    {protocol.HostKindCursor, "host-installation-cursor.json"},
		"workbuddy": {protocol.HostKindWorkBuddy, "host-installation-workbuddy.json"},
		"trae":      {protocol.HostKindTrae, "host-installation-trae.json"},
		"seal":      {protocol.HostKindSeal, "host-installation-seal.json"},
	}
	paths := make(map[string]bool)
	for name, want := range wants {
		target, err := parseHostTarget(name)
		if err != nil || target.kind != want.kind {
			t.Fatalf("parseHostTarget(%q)=%#v error=%v", name, target, err)
		}
		path, err := defaultCredentialPath(target.kind)
		if err != nil || filepath.Base(path) != want.file {
			t.Fatalf("defaultCredentialPath(%s)=%q error=%v", target.kind, path, err)
		}
		if paths[path] {
			t.Fatalf("Host credential path reused: %q", path)
		}
		paths[path] = true
	}
	alias, err := parseHostTarget("openclaw")
	if err != nil || alias.kind != protocol.HostKindSeal || alias.name != "openclaw" {
		t.Fatalf("parseHostTarget(openclaw)=%#v error=%v", alias, err)
	}
	lobiAlias, err := parseHostTarget("lobi")
	if err != nil || lobiAlias.kind != protocol.HostKindSeal || lobiAlias.name != "lobi" {
		t.Fatalf("parseHostTarget(lobi)=%#v error=%v", lobiAlias, err)
	}
}

func TestLoadCredentialFileRequiresPrivateRegularFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "host.json")
	body := `{"serverUrl":"https://agentx.example","credential":"secret","caller":{"principalId":"principal-1","spaceId":"space-1","hostInstallationId":"host-1","hostKind":"CODEX"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := hostauth.LoadCredentialBundle(path)
	if err != nil {
		t.Fatalf("loadCredentialFile: %v", err)
	}
	if value.Caller.HostInstallationID != "host-1" || value.Credential != "secret" {
		t.Fatalf("credential=%#v", value)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := hostauth.LoadCredentialBundle(path); err == nil {
		t.Fatal("loadCredentialFile accepted group/world-readable secret")
	}
}

func TestLoadCredentialFileRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "host.json")
	body := `{"serverUrl":"https://agentx.example","credential":"secret","unknown":true,"caller":{"principalId":"principal-1","spaceId":"space-1","hostInstallationId":"host-1","hostKind":"CODEX"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hostauth.LoadCredentialBundle(path); err == nil {
		t.Fatal("loadCredentialFile accepted unknown fields")
	}
	link := filepath.Join(directory, "host-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := hostauth.LoadCredentialBundle(link); err == nil {
		t.Fatal("loadCredentialFile accepted a symlink")
	}
}

func TestLoginCodexCompletesDeviceFlowAndPersistsPrivateCredential(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/agent-bridge/device-authorizations":
			if request.Method != http.MethodPost {
				t.Fatalf("begin method=%s", request.Method)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"deviceCode": "device-code-0123456789-0123456789", "userCode": "ABCDE-12345",
				"verificationUri": serverURLForRequest(request) + "/verify", "expiresAt": expiresAt,
				"intervalSeconds": 1,
			})
		case "/api/v1/agent-bridge/device-authorizations/token":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"credential": "team-secret-0123456789-0123456789", "expiresAt": expiresAt,
				"caller": map[string]any{
					"principalId": "principal-1", "spaceId": "space-1",
					"hostInstallationId": "host-1", "hostKind": "CODEX",
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	credentialPath := filepath.Join(t.TempDir(), "host.json")
	var output, errorOutput bytes.Buffer
	openedURL := ""
	dependencies := testCommandDependencies()
	dependencies.openBrowser = func(_ context.Context, value string) error {
		openedURL = value
		return nil
	}
	err := runWithDependencies([]string{
		"login", "codex", "--server", server.URL, "--credential-file", credentialPath,
		"--display-name", "Codex test installation",
	}, &output, &errorOutput, dependencies)
	if err != nil {
		t.Fatalf("run login: %v; stderr=%s", err, errorOutput.String())
	}
	if openedURL != server.URL+"/verify" {
		t.Fatalf("opened URL=%q, want verification URL", openedURL)
	}
	if strings.Contains(output.String(), "team-secret") || !strings.Contains(output.String(), "ABCDE-12345") {
		t.Fatalf("unsafe or incomplete output=%q", output.String())
	}
	value, err := hostauth.LoadCredentialBundle(credentialPath)
	if err != nil {
		t.Fatalf("load persisted credential: %v", err)
	}
	if value.ServerURL != server.URL || value.Credential != "team-secret-0123456789-0123456789" || value.Caller.HostInstallationID != "host-1" {
		t.Fatalf("credential=%#v", value)
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("stat credential: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode=%v, want 0600", info.Mode().Perm())
	}

	secondPath := filepath.Join(t.TempDir(), "host-no-browser.json")
	if err := runWithDependencies([]string{
		"login", "codex", "--server", server.URL, "--credential-file", secondPath,
		"--display-name", "Codex headless installation", "--no-open-browser",
	}, &output, &errorOutput, commandDependencies{
		probeMCP: successfulConformanceProbe,
		openBrowser: func(context.Context, string) error {
			t.Fatal("browser opener called with --no-open-browser")
			return nil
		},
	}); err != nil {
		t.Fatalf("run headless login: %v", err)
	}

	thirdPath := filepath.Join(t.TempDir(), "host-browser-fallback.json")
	var fallbackOutput, fallbackWarnings bytes.Buffer
	if err := runWithDependencies([]string{
		"login", "codex", "--server", server.URL, "--credential-file", thirdPath,
		"--display-name", "Codex browser fallback installation",
	}, &fallbackOutput, &fallbackWarnings, commandDependencies{
		probeMCP:    successfulConformanceProbe,
		openBrowser: func(context.Context, string) error { return errors.New("desktop unavailable") },
	}); err != nil {
		t.Fatalf("run browser fallback login: %v", err)
	}
	if !strings.Contains(fallbackOutput.String(), server.URL+"/verify") || !strings.Contains(fallbackWarnings.String(), "could not open") {
		t.Fatalf("fallback output=%q warnings=%q", fallbackOutput.String(), fallbackWarnings.String())
	}
}

func TestSaveCredentialBundleRequiresExplicitReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	value := hostauth.CredentialBundle{
		ServerURL: "https://agentx.example", Credential: "first-secret",
		Caller: protocol.CallerContext{PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex},
	}
	if err := hostauth.SaveCredentialBundle(path, value, false); err != nil {
		t.Fatal(err)
	}
	value.Credential = "second-secret"
	if err := hostauth.SaveCredentialBundle(path, value, false); err == nil {
		t.Fatal("SaveCredentialBundle replaced a credential without consent")
	}
	if err := hostauth.SaveCredentialBundle(path, value, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := hostauth.LoadCredentialBundle(path)
	if err != nil || loaded.Credential != "second-secret" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestCodexDoctorChecksInstallationCredentialAndServerSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer doctor-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	credentialPath := filepath.Join(t.TempDir(), "host.json")
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "doctor-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runCodexDoctor(context.Background(), staticCodexChecker{state: hostinstall.InstallationReady}, hostinstall.CodexSpec{}, credentialPath, &output, successfulConformanceProbe)
	if err != nil {
		t.Fatalf("runCodexDoctor: %v", err)
	}
	for _, evidence := range []string{"configuration: READY", "credential: READY", "authentication: READY", "capability search: READY", "process conformance: READY"} {
		if !strings.Contains(output.String(), evidence) {
			t.Fatalf("doctor output=%q, missing %q", output.String(), evidence)
		}
	}
}

func TestCodexDoctorFailsClosedBeforeServerCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := hostauth.SaveCredentialBundle(path, hostauth.CredentialBundle{
		ServerURL: "https://agentx.example", Credential: "expired-secret", ExpiresAt: &expiredAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCodexDoctor(context.Background(), staticCodexChecker{state: hostinstall.InstallationBroken}, hostinstall.CodexSpec{}, path, &output, successfulConformanceProbe); err == nil || !strings.Contains(err.Error(), "install codex") {
		t.Fatalf("doctor accepted broken installation: %v", err)
	}
	if err := runCodexDoctor(context.Background(), staticCodexChecker{state: hostinstall.InstallationReady}, hostinstall.CodexSpec{}, path, &output, successfulConformanceProbe); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("doctor accepted expired credential: %v", err)
	}
}

func TestWorkBuddyInstallDoctorAndUninstall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer workbuddy-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	directory := t.TempDir()
	bridgePath := filepath.Join(directory, "agentx-bridge-mcp")
	credentialPath := filepath.Join(directory, "workbuddy-host.json")
	configPath := filepath.Join(directory, ".workbuddy", "mcp.json")
	if err := os.WriteFile(bridgePath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "workbuddy-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-workbuddy-1", HostKind: protocol.HostKindWorkBuddy,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	common := []string{"--config-file", configPath, "--bridge-command", bridgePath, "--credential-file", credentialPath}
	var output bytes.Buffer
	if err := run(append([]string{"install", "workbuddy"}, common...), &output, &output); err != nil || !strings.Contains(output.String(), "READY") {
		t.Fatalf("install output=%q error=%v", output.String(), err)
	}
	if !strings.Contains(output.String(), "WorkBuddy Host activation: USER_ACTION_REQUIRED") {
		t.Fatalf("install output=%q does not disclose pending Host trust", output.String())
	}
	output.Reset()
	if err := runWithDependencies(append([]string{"doctor", "workbuddy"}, common...), &output, &output, testCommandDependencies()); err != nil || !strings.Contains(output.String(), "WorkBuddy MCP configuration: READY") {
		t.Fatalf("doctor output=%q error=%v", output.String(), err)
	}
	if !strings.Contains(output.String(), "WorkBuddy Host activation: USER_ACTION_REQUIRED") {
		t.Fatalf("doctor output=%q does not disclose pending Host trust", output.String())
	}
	output.Reset()
	if err := run(append([]string{"uninstall", "workbuddy"}, common...), &output, &output); err != nil || !strings.Contains(output.String(), "MISSING") {
		t.Fatalf("uninstall output=%q error=%v", output.String(), err)
	}
}

func TestTraeInstallDoctorAndUninstall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer trae-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	directory := t.TempDir()
	bridgePath := filepath.Join(directory, "agentx-bridge-mcp")
	credentialPath := filepath.Join(directory, "trae-host.json")
	configPath := filepath.Join(directory, ".trae", "mcp.json")
	if err := os.WriteFile(bridgePath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "trae-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-trae-1", HostKind: protocol.HostKindTrae,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	common := []string{"--config-file", configPath, "--bridge-command", bridgePath, "--credential-file", credentialPath}
	var output bytes.Buffer
	if err := run(append([]string{"install", "trae"}, common...), &output, &output); err != nil || !strings.Contains(output.String(), "READY") {
		t.Fatalf("install output=%q error=%v", output.String(), err)
	}
	output.Reset()
	if err := runWithDependencies(append([]string{"doctor", "trae"}, common...), &output, &output, testCommandDependencies()); err != nil || !strings.Contains(output.String(), "Trae MCP configuration: READY") {
		t.Fatalf("doctor output=%q error=%v", output.String(), err)
	}
	output.Reset()
	if err := run(append([]string{"uninstall", "trae"}, common...), &output, &output); err != nil || !strings.Contains(output.String(), "MISSING") {
		t.Fatalf("uninstall output=%q error=%v", output.String(), err)
	}
}

func TestConnectCursorReusesCredentialInstallsAndRunsDoctor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer cursor-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	directory := t.TempDir()
	bridgePath := filepath.Join(directory, "agentx-bridge-mcp")
	credentialPath := filepath.Join(directory, "cursor-host.json")
	configPath := filepath.Join(directory, ".cursor", "mcp.json")
	if err := os.WriteFile(bridgePath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "cursor-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-cursor-1", HostKind: protocol.HostKindCursor,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runWithDependencies([]string{
		"connect", "cursor", "--server", server.URL, "--credential-file", credentialPath,
		"--bridge-command", bridgePath, "--config-file", configPath,
	}, &output, &output, testCommandDependencies())
	if err != nil {
		t.Fatalf("connect cursor: %v output=%q", err, output.String())
	}
	for _, evidence := range []string{"credential: REUSED", "Cursor MCP: READY", "capability search: READY", "connection: READY"} {
		if !strings.Contains(output.String(), evidence) {
			t.Fatalf("connect output=%q missing %q", output.String(), evidence)
		}
	}
}

func TestConnectWorkBuddyStopsAtHostTrustGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer workbuddy-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	directory := t.TempDir()
	bridgePath := filepath.Join(directory, "agentx-bridge-mcp")
	credentialPath := filepath.Join(directory, "workbuddy-host.json")
	configPath := filepath.Join(directory, ".workbuddy", "mcp.json")
	if err := os.WriteFile(bridgePath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "workbuddy-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-workbuddy-1", HostKind: protocol.HostKindWorkBuddy,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runWithDependencies([]string{
		"connect", "workbuddy", "--server", server.URL, "--credential-file", credentialPath,
		"--bridge-command", bridgePath, "--config-file", configPath,
	}, &output, &output, testCommandDependencies())
	if err != nil {
		t.Fatalf("connect workbuddy: %v output=%q", err, output.String())
	}
	for _, evidence := range []string{
		"credential: REUSED", "WorkBuddy MCP: READY", "capability search: READY",
		"Host activation: USER_ACTION_REQUIRED", "connection: READY_FOR_HOST_TRUST",
	} {
		if !strings.Contains(output.String(), evidence) {
			t.Fatalf("connect output=%q missing %q", output.String(), evidence)
		}
	}
	if strings.Contains(output.String(), "connection: READY\n") {
		t.Fatalf("connect output=%q falsely claims real Host readiness", output.String())
	}
}

func TestConnectSealStopsAtRemotePluginInstallGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agent-bridge/capabilities/search" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer seal-secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(protocol.SearchCapabilitiesResponse{
			ContractVersion: protocol.ContractVersion, Disposition: protocol.SearchDispositionCapabilityGap,
			NextAction: protocol.SearchNextActionAskUserOrReportGap, SafeSummary: "No eligible capability was found.",
		})
	}))
	defer server.Close()
	directory := t.TempDir()
	bridgePath := filepath.Join(directory, "agentx-bridge-mcp")
	credentialPath := filepath.Join(directory, "seal-host.json")
	if err := os.WriteFile(bridgePath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := hostauth.SaveCredentialBundle(credentialPath, hostauth.CredentialBundle{
		ServerURL: server.URL, Credential: "seal-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-seal-1", HostKind: protocol.HostKindSeal,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runWithDependencies([]string{
		"connect", "seal", "--server", server.URL, "--credential-file", credentialPath,
		"--bridge-command", bridgePath,
	}, &output, &output, testCommandDependencies())
	if err != nil {
		t.Fatalf("connect seal: %v output=%q", err, output.String())
	}
	for _, evidence := range []string{
		"credential: REUSED", "Seal/OpenClaw local Bridge MCP configuration: READY",
		"capability search: READY", "Host activation: USER_ACTION_REQUIRED",
		"connection: READY_FOR_HOST_PLUGIN_INSTALL",
	} {
		if !strings.Contains(output.String(), evidence) {
			t.Fatalf("connect output=%q missing %q", output.String(), evidence)
		}
	}
	if strings.Contains(output.String(), "connection: READY\n") {
		t.Fatalf("connect output=%q falsely claims remote Seal/Lobi readiness", output.String())
	}
}

func TestSealInstallDoesNotPretendToManageRemoteLobi(t *testing.T) {
	var output bytes.Buffer
	err := run([]string{"install", "seal"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "does not install or remove Seal/OpenClaw/Lobi plugins") {
		t.Fatalf("install seal error=%v output=%q", err, output.String())
	}
}

func testCommandDependencies() commandDependencies {
	return commandDependencies{
		probeMCP:    successfulConformanceProbe,
		openBrowser: func(context.Context, string) error { return nil },
	}
}

func successfulConformanceProbe(_ context.Context, spec hostconformance.CommandSpec) (hostconformance.Evidence, error) {
	profile := spec.Profile
	if profile == "" {
		profile = mcpserver.ToolProfileFull
	}
	toolCount := len(protocol.ToolSurface())
	if profile == mcpserver.ToolProfileCompact {
		toolCount = len(protocol.CompactToolSurface())
	}
	return hostconformance.Evidence{Profile: profile, ToolCount: toolCount}, nil
}

func TestReusableHostCredentialRejectsExpiredAndWrongHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	expiresAt := time.Now().UTC().Add(-time.Minute)
	if err := hostauth.SaveCredentialBundle(path, hostauth.CredentialBundle{
		ServerURL: "https://agentx.example", Credential: "expired-secret", ExpiresAt: &expiresAt,
		Caller: protocol.CallerContext{
			PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex,
		},
	}, false); err != nil {
		t.Fatal(err)
	}
	if reused, err := reusableHostCredential(path, "https://agentx.example", protocol.HostKindCodex); reused || err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired reused=%t error=%v", reused, err)
	}
	if reused, err := reusableHostCredential(path, "https://agentx.example", protocol.HostKindCursor); reused || err == nil || !strings.Contains(err.Error(), "belongs") {
		t.Fatalf("wrong Host reused=%t error=%v", reused, err)
	}
}

func TestVersionDoesNotRequireHostDependencies(t *testing.T) {
	previousVersion, previousCommit := buildVersion, buildCommit
	buildVersion, buildCommit = "0.1.0-test", "abcdef123456"
	t.Cleanup(func() { buildVersion, buildCommit = previousVersion, previousCommit })
	var output bytes.Buffer
	if err := runWithDependencies([]string{"version"}, &output, &output, commandDependencies{}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if output.String() != "agentx-bridge-mcp 0.1.0-test (abcdef123456)\n" {
		t.Fatalf("version output=%q", output.String())
	}
	if err := runWithDependencies([]string{"version", "extra"}, &output, &output, commandDependencies{}); err == nil {
		t.Fatal("version accepted unexpected arguments")
	}
}

func TestPluginLoginReusesIdentityBeforeDeviceSideEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	expires := time.Now().UTC().Add(time.Hour)
	value := hostauth.CredentialBundle{ServerURL: "https://agentx.example", Credential: "test-only-reused-credential", ExpiresAt: &expires,
		Caller: protocol.CallerContext{PrincipalID: "principal-test", SpaceID: "space-test", HostInstallationID: "host-test", HostKind: protocol.HostKindCodex}}
	if err := hostauth.SaveCredentialBundle(path, value, false); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"login", "codex", "--server", value.ServerURL, "--credential-file", path, "--reuse"}
	if err := runWithDependencies(args, &output, &output, testCommandDependencies()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "credential reused") || strings.Contains(output.String(), value.Credential) {
		t.Fatal("invalid reuse output")
	}
	if err := runWithDependencies(args[:len(args)-1], &output, &output, testCommandDependencies()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("did not reject before network: %v", err)
	}
}

type staticCodexChecker struct {
	state hostinstall.InstallationState
	err   error
}

func (checker staticCodexChecker) Check(context.Context, hostinstall.CodexSpec) (hostinstall.InstallationState, error) {
	return checker.state, checker.err
}

func serverURLForRequest(request *http.Request) string {
	return "http://" + request.Host
}
