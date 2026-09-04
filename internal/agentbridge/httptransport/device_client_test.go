package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostauth"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

func TestDeviceClientBeginAndPoll(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour)
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case DeviceAuthorizationPath:
			_ = json.NewEncoder(writer).Encode(hostauth.DeviceAuthorizationStart{
				DeviceCode: "device-code-0123456789-0123456789", UserCode: "ABCDE-12345", VerificationURI: "http://localhost/verify",
				ExpiresAt: expiresAt, IntervalSeconds: 1,
			})
		case DeviceAuthorizationTokenPath:
			polls++
			if polls == 1 {
				writer.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(writer).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(writer).Encode(hostauth.DeviceAuthorizationToken{
				Credential: "credential-0123456789-0123456789", ExpiresAt: expiresAt,
				Caller: protocol.CallerContext{PrincipalID: "principal-1", SpaceID: "space-1", HostInstallationID: "host-1", HostKind: protocol.HostKindCodex},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := NewDeviceClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, err := client.Begin(context.Background(), protocol.HostKindCodex, "Codex on test")
	if err != nil || started.UserCode != "ABCDE-12345" {
		t.Fatalf("started=%#v err=%v", started, err)
	}
	if _, err := client.Poll(context.Background(), started.DeviceCode); !errors.Is(err, hostauth.ErrAuthorizationPending) {
		t.Fatalf("first poll err=%v", err)
	}
	token, err := client.Poll(context.Background(), started.DeviceCode)
	if err != nil || token.Caller.HostInstallationID != "host-1" {
		t.Fatalf("token=%#v err=%v", token, err)
	}
}

func TestDeviceClientReturnsOnlyBoundedRemoteErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_request","errorDescription":"do not echo this detail"}`))
	}))
	defer server.Close()
	client, err := NewDeviceClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Begin(context.Background(), protocol.HostKindWorkBuddy, "WorkBuddy")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "invalid_request") || strings.Contains(err.Error(), "do not echo") {
		t.Fatalf("safe remote error=%v", err)
	}

	unsafe := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"<script>alert(1)</script>"}`))
	}))
	defer unsafe.Close()
	client, err = NewDeviceClient(unsafe.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Begin(context.Background(), protocol.HostKindWorkBuddy, "WorkBuddy")
	if err == nil || strings.Contains(err.Error(), "script") {
		t.Fatalf("unsafe remote error=%v", err)
	}
}

func TestDeviceClientRejectsRedirectAndUnsafeVerificationURI(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://attacker.example/device", http.StatusFound)
	}))
	defer redirect.Close()
	client, err := NewDeviceClient(redirect.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Begin(context.Background(), protocol.HostKindCodex, "Codex"); err == nil {
		t.Fatal("DeviceClient followed or accepted a redirect")
	}

	unsafe := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(hostauth.DeviceAuthorizationStart{
			DeviceCode: "device-code-0123456789-0123456789", UserCode: "ABCDE-12345", VerificationURI: "http://attacker.example/verify",
			ExpiresAt: time.Now().UTC().Add(time.Hour), IntervalSeconds: 1,
		})
	}))
	defer unsafe.Close()
	client, err = NewDeviceClient(unsafe.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Begin(context.Background(), protocol.HostKindCodex, "Codex"); err == nil {
		t.Fatal("DeviceClient accepted an insecure verification URI")
	}
}
