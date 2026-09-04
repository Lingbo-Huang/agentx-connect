package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostauth"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

const maxDeviceResponseBytes = 64 << 10

const (
	DeviceAuthorizationPath      = "/api/v1/agent-bridge/device-authorizations"
	DeviceAuthorizationTokenPath = "/api/v1/agent-bridge/device-authorizations/token"
)

// DeviceClient implements the public, unauthenticated half of the OAuth-style
// Team Host device flow. It never sends an existing Host bearer credential and
// refuses redirects so a device code cannot be forwarded to another origin.
type DeviceClient struct {
	serverURL     string
	beginEndpoint string
	tokenEndpoint string
	http          *http.Client
}

func NewDeviceClient(serverURL string, httpClient *http.Client) (*DeviceClient, error) {
	origin, err := validateServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	} else {
		clone := *httpClient
		httpClient = &clone
	}
	if httpClient.Timeout <= 0 {
		httpClient.Timeout = defaultClientTimeout
	}
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	base := strings.TrimSuffix(origin.String(), "/")
	return &DeviceClient{
		serverURL: base, beginEndpoint: base + DeviceAuthorizationPath,
		tokenEndpoint: base + DeviceAuthorizationTokenPath, http: httpClient,
	}, nil
}

func (client *DeviceClient) Begin(ctx context.Context, hostKind protocol.HostKind, displayName string) (hostauth.DeviceAuthorizationStart, error) {
	displayName = strings.TrimSpace(displayName)
	if !clientHostKind(hostKind) || displayName == "" || len(displayName) > 160 {
		return hostauth.DeviceAuthorizationStart{}, errors.New("Host device authorization request is invalid")
	}
	body, err := json.Marshal(map[string]any{"hostKind": hostKind, "displayName": displayName})
	if err != nil {
		return hostauth.DeviceAuthorizationStart{}, errors.New("prepare Host device authorization request")
	}
	status, responseBody, err := client.postJSON(ctx, client.beginEndpoint, body)
	if err != nil {
		return hostauth.DeviceAuthorizationStart{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return hostauth.DeviceAuthorizationStart{}, deviceAuthorizationResponseError(status, responseBody)
	}
	var started hostauth.DeviceAuthorizationStart
	if json.Unmarshal(responseBody, &started) != nil || validateAuthorizationStart(started) != nil {
		return hostauth.DeviceAuthorizationStart{}, errors.New("AgentX Server returned an invalid Host device authorization")
	}
	return started, nil
}

func (client *DeviceClient) Poll(ctx context.Context, deviceCode string) (hostauth.DeviceAuthorizationToken, error) {
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationNotFound
	}
	body, err := json.Marshal(map[string]string{"deviceCode": deviceCode})
	if err != nil {
		return hostauth.DeviceAuthorizationToken{}, errors.New("prepare Host device authorization exchange")
	}
	status, responseBody, err := client.postJSON(ctx, client.tokenEndpoint, body)
	if err != nil {
		return hostauth.DeviceAuthorizationToken{}, err
	}
	if status == http.StatusOK {
		var token hostauth.DeviceAuthorizationToken
		if json.Unmarshal(responseBody, &token) != nil || validateAuthorizationToken(token) != nil {
			return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX Server returned an invalid Host credential")
		}
		return token, nil
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(responseBody, &failure) != nil {
		return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX Server rejected Host device authorization")
	}
	switch failure.Error {
	case "authorization_pending":
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationPending
	case "slow_down":
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationSlowDown
	case "access_denied":
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationDenied
	case "expired_token":
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationExpired
	case "invalid_grant":
		return hostauth.DeviceAuthorizationToken{}, hostauth.ErrAuthorizationConsumed
	default:
		return hostauth.DeviceAuthorizationToken{}, deviceAuthorizationResponseError(status, responseBody)
	}
}

func deviceAuthorizationResponseError(status int, body []byte) error {
	base := fmt.Sprintf("AgentX Server rejected Host device authorization (HTTP %d)", status)
	var response struct {
		Code string `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil || !safeRemoteErrorCode(response.Code) {
		return errors.New(base)
	}
	return fmt.Errorf("%s: %s", base, response.Code)
}

func safeRemoteErrorCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func (client *DeviceClient) postJSON(ctx context.Context, endpoint string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("prepare Host device authorization request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "agentx-bridge-mcp/0.1")
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, errors.New("AgentX Server is unavailable")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxDeviceResponseBytes+1))
	if err != nil || len(responseBody) > maxDeviceResponseBytes {
		return 0, nil, errors.New("AgentX Server returned an invalid Host authorization response")
	}
	return response.StatusCode, responseBody, nil
}

func validateAuthorizationStart(started hostauth.DeviceAuthorizationStart) error {
	deviceCode, userCode := strings.TrimSpace(started.DeviceCode), strings.TrimSpace(started.UserCode)
	if len(deviceCode) < 32 || len(deviceCode) > 256 || len(userCode) < 8 || len(userCode) > 32 || started.IntervalSeconds <= 0 || started.IntervalSeconds > 30 || !started.ExpiresAt.After(time.Now().UTC()) {
		return errors.New("Host device authorization response is incomplete")
	}
	verificationURI, err := url.Parse(strings.TrimSpace(started.VerificationURI))
	if err != nil || verificationURI.Host == "" || verificationURI.User != nil || verificationURI.Fragment != "" {
		return errors.New("Host device verification URI is invalid")
	}
	if verificationURI.Scheme == "https" {
		return nil
	}
	if verificationURI.Scheme == "http" && (verificationURI.Hostname() == "localhost" || verificationURI.Hostname() == "127.0.0.1" || verificationURI.Hostname() == "::1") {
		return nil
	}
	return errors.New("Host device verification URI is insecure")
}

func validateAuthorizationToken(token hostauth.DeviceAuthorizationToken) error {
	credential := strings.TrimSpace(token.Credential)
	if len(credential) < 32 || len(credential) > 512 || !token.ExpiresAt.After(time.Now().UTC()) {
		return errors.New("Host device credential is incomplete")
	}
	return protocol.ValidateCallerContext(token.Caller)
}

func clientHostKind(value protocol.HostKind) bool {
	switch value {
	case protocol.HostKindCodex, protocol.HostKindClaude, protocol.HostKindSeal, protocol.HostKindCursor, protocol.HostKindWorkBuddy, protocol.HostKindTrae, protocol.HostKindOther:
		return true
	default:
		return false
	}
}
