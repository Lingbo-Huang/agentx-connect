// Package httptransport carries Agent Bridge requests between a local Host
// MCP process and AgentX Server. It is deliberately narrower than the public
// Agent Bridge contract: caller identity is derived from the bearer credential
// at the Server and is never accepted from the JSON body.
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

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

const (
	SearchPath             = "/api/v1/agent-bridge/capabilities/search"
	InvokePath             = "/api/v1/agent-bridge/capabilities/invoke"
	HandoffPath            = "/api/v1/agent-bridge/handoffs"
	HandoffListPath        = "/api/v1/agent-bridge/handoffs/list"
	HandoffStatusPath      = "/api/v1/agent-bridge/handoffs/status"
	HandoffArtifactPath    = "/api/v1/agent-bridge/handoffs/artifacts/fetch"
	HandoffAcceptancePath  = "/api/v1/agent-bridge/handoffs/acceptances"
	HandoffRejectionPath   = "/api/v1/agent-bridge/handoffs/rejections"
	maxSearchRequestBytes  = 32 << 10
	maxInvokeRequestBytes  = protocol.MaxInvokeInputBytes + (32 << 10)
	maxHandoffRequestBytes = 256 << 10
	maxResponseBytes       = 1 << 20
	defaultClientTimeout   = 30 * time.Second
)

type SearchBackend interface {
	Search(context.Context, protocol.SearchCapabilitiesRequest) (protocol.SearchCapabilitiesResponse, error)
}

type InvokeBackend interface {
	Invoke(context.Context, protocol.InvokeCapabilityRequest) (protocol.InvocationReceipt, error)
}

type HandoffBackend interface {
	Handoff(context.Context, protocol.HandoffWorkRequest) (protocol.HandoffRef, error)
	List(context.Context, protocol.ListHandoffsRequest) (protocol.ListHandoffsResponse, error)
	Status(context.Context, protocol.GetHandoffStatusRequest) (protocol.HandoffStatusView, error)
	FetchArtifact(context.Context, protocol.FetchArtifactRequest) (protocol.ArtifactReference, error)
	AcceptDelivery(context.Context, protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error)
	RejectDelivery(context.Context, protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error)
}

type Authenticator interface {
	AuthenticateHost(context.Context, string) (protocol.CallerContext, error)
}

type HandlerConfig struct {
	Search       SearchBackend
	Invoke       InvokeBackend
	Handoff      HandoffBackend
	Authenticate Authenticator
}

type searchInput struct {
	Query                 string               `json:"query"`
	ExpectedArtifactTypes []string             `json:"expectedArtifactTypes,omitempty"`
	MaximumSensitivity    protocol.Sensitivity `json:"maximumSensitivity"`
	Mode                  protocol.RoutingMode `json:"mode"`
	BudgetMaximumMinor    *int64               `json:"budgetMaximumMinor,omitempty"`
	BudgetCurrency        string               `json:"budgetCurrency,omitempty"`
	Deadline              *time.Time           `json:"deadline,omitempty"`
	Limit                 uint32               `json:"limit,omitempty"`
}

type invokeInput struct {
	BindingID      string          `json:"bindingId"`
	BindingVersion string          `json:"bindingVersion"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Input          json.RawMessage `json:"input"`
}

type handoffInput struct {
	BindingID         string                         `json:"bindingId"`
	BindingVersion    string                         `json:"bindingVersion"`
	IdempotencyKey    string                         `json:"idempotencyKey"`
	Goal              string                         `json:"goal"`
	Context           []protocol.ContextReference    `json:"context,omitempty"`
	ExpectedArtifacts []protocol.ArtifactExpectation `json:"expectedArtifacts"`
	Deadline          *time.Time                     `json:"deadline,omitempty"`
}

type handoffStatusInput struct {
	HandoffID         string `json:"handoffId"`
	KnownUpdateCursor string `json:"knownUpdateCursor,omitempty"`
}

type handoffListInput struct {
	Limit uint32 `json:"limit,omitempty"`
}

type artifactInput struct {
	HandoffID  string `json:"handoffId"`
	ArtifactID string `json:"artifactId"`
}

type deliveryDecisionInput struct {
	HandoffID      string `json:"handoffId"`
	MissionVersion uint64 `json:"missionVersion"`
	IdempotencyKey string `json:"idempotencyKey"`
	Reason         string `json:"reason,omitempty"`
}

func NewHandler(config HandlerConfig) (http.Handler, error) {
	if config.Search == nil || config.Authenticate == nil {
		return nil, errors.New("Agent Bridge HTTP transport requires search and Host authentication")
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || (request.URL.Path != SearchPath && request.URL.Path != InvokePath &&
			request.URL.Path != HandoffPath && request.URL.Path != HandoffListPath && request.URL.Path != HandoffStatusPath && request.URL.Path != HandoffArtifactPath &&
			request.URL.Path != HandoffAcceptancePath && request.URL.Path != HandoffRejectionPath) {
			http.NotFound(writer, request)
			return
		}
		credential, ok := bearerCredential(request.Header.Get("Authorization"))
		if !ok {
			writeError(writer, http.StatusUnauthorized, protocol.NewError(protocol.ErrorCodeAuthenticationRequired, "Sign in to AgentX from this Host before searching capabilities", false, true, ""))
			return
		}
		caller, err := config.Authenticate.AuthenticateHost(request.Context(), credential)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, hostSafeError(err, protocol.ErrorCodeAuthenticationRequired, "Host authentication is invalid or expired"))
			return
		}
		if request.URL.Path == InvokePath {
			if config.Invoke == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxInvokeRequestBytes)
			var input invokeInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Capability invocation request is invalid", false, false, ""))
				return
			}
			receipt, err := config.Invoke.Invoke(request.Context(), protocol.InvokeCapabilityRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller,
				BindingID: input.BindingID, BindingVersion: input.BindingVersion,
				IdempotencyKey: input.IdempotencyKey, Input: input.Input,
			})
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Capability invocation is temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusOK, receipt)
			return
		}
		if request.URL.Path == HandoffPath {
			if config.Handoff == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxHandoffRequestBytes)
			var input handoffInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Handoff request is invalid", false, false, ""))
				return
			}
			reference, err := config.Handoff.Handoff(request.Context(), protocol.HandoffWorkRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller, BindingID: input.BindingID,
				BindingVersion: input.BindingVersion, IdempotencyKey: input.IdempotencyKey, Goal: input.Goal,
				Context: input.Context, ExpectedArtifacts: input.ExpectedArtifacts, Deadline: input.Deadline,
			})
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Handoff is temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusAccepted, reference)
			return
		}
		if request.URL.Path == HandoffStatusPath {
			if config.Handoff == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxSearchRequestBytes)
			var input handoffStatusInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Handoff status request is invalid", false, false, ""))
				return
			}
			view, err := config.Handoff.Status(request.Context(), protocol.GetHandoffStatusRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller, HandoffID: input.HandoffID,
				KnownUpdateCursor: input.KnownUpdateCursor,
			})
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Handoff status is temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusOK, view)
			return
		}
		if request.URL.Path == HandoffListPath {
			if config.Handoff == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxSearchRequestBytes)
			var input handoffListInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Handoff list request is invalid", false, false, ""))
				return
			}
			response, err := config.Handoff.List(request.Context(), protocol.ListHandoffsRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller, Limit: input.Limit,
			})
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Recent handoffs are temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusOK, response)
			return
		}
		if request.URL.Path == HandoffArtifactPath {
			if config.Handoff == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxSearchRequestBytes)
			var input artifactInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Artifact request is invalid", false, false, ""))
				return
			}
			reference, err := config.Handoff.FetchArtifact(request.Context(), protocol.FetchArtifactRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller, HandoffID: input.HandoffID, ArtifactID: input.ArtifactID,
			})
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Handoff Artifact is temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusOK, reference)
			return
		}
		if request.URL.Path == HandoffAcceptancePath || request.URL.Path == HandoffRejectionPath {
			if config.Handoff == nil {
				http.NotFound(writer, request)
				return
			}
			request.Body = http.MaxBytesReader(writer, request.Body, maxSearchRequestBytes)
			var input deliveryDecisionInput
			if err := decodeSingleJSON(request.Body, &input); err != nil {
				writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Delivery decision request is invalid", false, false, ""))
				return
			}
			decision := protocol.DeliveryDecisionRequest{
				ContractVersion: protocol.ContractVersion, Caller: caller, HandoffID: input.HandoffID,
				MissionVersion: input.MissionVersion, IdempotencyKey: input.IdempotencyKey, Reason: input.Reason,
			}
			var receipt protocol.DeliveryDecisionReceipt
			var err error
			if request.URL.Path == HandoffAcceptancePath {
				receipt, err = config.Handoff.AcceptDelivery(request.Context(), decision)
			} else {
				receipt, err = config.Handoff.RejectDelivery(request.Context(), decision)
			}
			if err != nil {
				bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Delivery decision is temporarily unavailable")
				writeError(writer, statusForError(bridgeErr), bridgeErr)
				return
			}
			writeJSON(writer, http.StatusOK, receipt)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxSearchRequestBytes)
		var input searchInput
		if err := decodeSingleJSON(request.Body, &input); err != nil {
			writeError(writer, http.StatusBadRequest, protocol.NewError(protocol.ErrorCodeValidationFailed, "Capability search request is invalid", false, false, ""))
			return
		}
		response, err := config.Search.Search(request.Context(), protocol.SearchCapabilitiesRequest{
			ContractVersion: protocol.ContractVersion,
			Caller:          caller,
			Query:           input.Query, ExpectedArtifactTypes: input.ExpectedArtifactTypes,
			MaximumSensitivity: input.MaximumSensitivity, Mode: input.Mode,
			BudgetMaximumMinor: input.BudgetMaximumMinor, BudgetCurrency: input.BudgetCurrency,
			Deadline: input.Deadline, Limit: input.Limit,
		})
		if err != nil {
			bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "Capability search is temporarily unavailable")
			writeError(writer, statusForError(bridgeErr), bridgeErr)
			return
		}
		writeJSON(writer, http.StatusOK, response)
	}), nil
}

type ClientConfig struct {
	ServerURL  string
	Credential string
	HTTPClient *http.Client
}

type Client struct {
	serverOrigin              *url.URL
	searchEndpoint            string
	invokeEndpoint            string
	handoffEndpoint           string
	handoffListEndpoint       string
	handoffStatusEndpoint     string
	handoffArtifactEndpoint   string
	handoffAcceptanceEndpoint string
	handoffRejectionEndpoint  string
	credential                string
	http                      *http.Client
}

func NewClient(config ClientConfig) (*Client, error) {
	serverURL, err := validateServerURL(config.ServerURL)
	if err != nil {
		return nil, err
	}
	credential := strings.TrimSpace(config.Credential)
	if credential == "" {
		return nil, errors.New("Agent Bridge Host credential is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	} else {
		clone := *httpClient
		httpClient = &clone
	}
	if httpClient.Timeout <= 0 {
		httpClient.Timeout = defaultClientTimeout
	}
	// A redirect must never carry the Host bearer credential to another
	// origin. Enforce this even when a caller supplies its own HTTP client.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	origin := strings.TrimSuffix(serverURL.String(), "/")
	return &Client{
		serverOrigin:   serverURL,
		searchEndpoint: origin + SearchPath, invokeEndpoint: origin + InvokePath,
		handoffEndpoint: origin + HandoffPath, handoffListEndpoint: origin + HandoffListPath, handoffStatusEndpoint: origin + HandoffStatusPath,
		handoffArtifactEndpoint: origin + HandoffArtifactPath, handoffAcceptanceEndpoint: origin + HandoffAcceptancePath,
		handoffRejectionEndpoint: origin + HandoffRejectionPath,
		credential:               credential, http: httpClient,
	}, nil
}

func (client *Client) Search(ctx context.Context, request protocol.SearchCapabilitiesRequest) (protocol.SearchCapabilitiesResponse, error) {
	if err := protocol.ValidateSearchCapabilitiesRequest(request); err != nil {
		return protocol.SearchCapabilitiesResponse{}, err
	}
	body, err := json.Marshal(searchInput{
		Query: request.Query, ExpectedArtifactTypes: request.ExpectedArtifactTypes,
		MaximumSensitivity: request.MaximumSensitivity, Mode: request.Mode,
		BudgetMaximumMinor: request.BudgetMaximumMinor, BudgetCurrency: request.BudgetCurrency,
		Deadline: request.Deadline, Limit: request.Limit,
	})
	if err != nil {
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability search request could not be prepared", false, false, "")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.searchEndpoint, bytes.NewReader(body))
	if err != nil {
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability search request could not be prepared", false, false, "")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.credential)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "agentx-bridge-mcp/0.1")
	response, err := client.http.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return protocol.SearchCapabilitiesResponse{}, ctx.Err()
		}
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeProviderUnavailable, "AgentX Server is unavailable", true, false, "")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil || len(responseBody) > maxResponseBytes {
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid response", true, false, "")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var bridgeErr protocol.Error
		if json.Unmarshal(responseBody, &bridgeErr) == nil && bridgeErr.Code != "" {
			return protocol.SearchCapabilitiesResponse{}, &bridgeErr
		}
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server rejected capability search", response.StatusCode >= 500, response.StatusCode == http.StatusUnauthorized, "")
	}
	var result protocol.SearchCapabilitiesResponse
	if err := json.Unmarshal(responseBody, &result); err != nil || protocol.ValidateSearchCapabilitiesResponse(result) != nil {
		return protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid capability result", true, false, "")
	}
	return result, nil
}

func (client *Client) Invoke(ctx context.Context, request protocol.InvokeCapabilityRequest) (protocol.InvocationReceipt, error) {
	if err := protocol.ValidateInvokeCapabilityRequest(request); err != nil {
		return protocol.InvocationReceipt{}, err
	}
	body, err := json.Marshal(invokeInput{
		BindingID: request.BindingID, BindingVersion: request.BindingVersion,
		IdempotencyKey: request.IdempotencyKey, Input: request.Input,
	})
	if err != nil {
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability invocation request could not be prepared", false, false, "")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.invokeEndpoint, bytes.NewReader(body))
	if err != nil {
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability invocation request could not be prepared", false, false, "")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.credential)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "agentx-bridge-mcp/0.1")
	response, err := client.http.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return protocol.InvocationReceipt{}, ctx.Err()
		}
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeProviderUnavailable, "AgentX Server is unavailable", true, false, "")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid response", true, false, "")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var bridgeErr protocol.Error
		if json.Unmarshal(responseBody, &bridgeErr) == nil && bridgeErr.Code != "" {
			return protocol.InvocationReceipt{}, &bridgeErr
		}
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server rejected capability invocation", response.StatusCode >= 500, response.StatusCode == http.StatusUnauthorized, "")
	}
	var receipt protocol.InvocationReceipt
	if err := json.Unmarshal(responseBody, &receipt); err != nil || protocol.ValidateInvocationReceipt(receipt) != nil {
		return protocol.InvocationReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid Invocation Receipt", true, false, "")
	}
	return receipt, nil
}

func (client *Client) Handoff(ctx context.Context, request protocol.HandoffWorkRequest) (protocol.HandoffRef, error) {
	if err := protocol.ValidateHandoffWorkRequest(request); err != nil {
		return protocol.HandoffRef{}, err
	}
	body, err := json.Marshal(handoffInput{
		BindingID: request.BindingID, BindingVersion: request.BindingVersion, IdempotencyKey: request.IdempotencyKey,
		Goal: request.Goal, Context: request.Context, ExpectedArtifacts: request.ExpectedArtifacts, Deadline: request.Deadline,
	})
	if err != nil {
		return protocol.HandoffRef{}, protocol.NewError(protocol.ErrorCodeInternal, "Handoff request could not be prepared", false, false, "")
	}
	responseBody, status, err := client.post(ctx, client.handoffEndpoint, body)
	if err != nil {
		return protocol.HandoffRef{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return protocol.HandoffRef{}, client.decodeBridgeError(responseBody, status, "AgentX Server rejected the handoff")
	}
	var reference protocol.HandoffRef
	if json.Unmarshal(responseBody, &reference) != nil || protocol.ValidateHandoffRef(reference) != nil {
		return protocol.HandoffRef{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid Handoff reference", true, false, "")
	}
	reference.DeepLink = client.resolveSameOriginReference(reference.DeepLink)
	return reference, nil
}

func (client *Client) Status(ctx context.Context, request protocol.GetHandoffStatusRequest) (protocol.HandoffStatusView, error) {
	if err := protocol.ValidateGetHandoffStatusRequest(request); err != nil {
		return protocol.HandoffStatusView{}, err
	}
	body, err := json.Marshal(handoffStatusInput{HandoffID: request.HandoffID, KnownUpdateCursor: request.KnownUpdateCursor})
	if err != nil {
		return protocol.HandoffStatusView{}, protocol.NewError(protocol.ErrorCodeInternal, "Handoff status request could not be prepared", false, false, request.HandoffID)
	}
	responseBody, status, err := client.post(ctx, client.handoffStatusEndpoint, body)
	if err != nil {
		return protocol.HandoffStatusView{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return protocol.HandoffStatusView{}, client.decodeBridgeError(responseBody, status, "AgentX Server rejected the Handoff status request")
	}
	var view protocol.HandoffStatusView
	if json.Unmarshal(responseBody, &view) != nil || protocol.ValidateHandoffStatusView(view) != nil {
		return protocol.HandoffStatusView{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid Handoff status", true, false, request.HandoffID)
	}
	view.DeepLink = client.resolveSameOriginReference(view.DeepLink)
	return view, nil
}

func (client *Client) List(ctx context.Context, request protocol.ListHandoffsRequest) (protocol.ListHandoffsResponse, error) {
	if err := protocol.ValidateListHandoffsRequest(request); err != nil {
		return protocol.ListHandoffsResponse{}, err
	}
	body, err := json.Marshal(handoffListInput{Limit: request.Limit})
	if err != nil {
		return protocol.ListHandoffsResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "Handoff list request could not be prepared", false, false, "")
	}
	responseBody, status, err := client.post(ctx, client.handoffListEndpoint, body)
	if err != nil {
		return protocol.ListHandoffsResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return protocol.ListHandoffsResponse{}, client.decodeBridgeError(responseBody, status, "AgentX Server rejected the Handoff list request")
	}
	var response protocol.ListHandoffsResponse
	if json.Unmarshal(responseBody, &response) != nil || protocol.ValidateListHandoffsResponse(response) != nil {
		return protocol.ListHandoffsResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid Handoff list", true, false, "")
	}
	for index := range response.Items {
		response.Items[index].DeepLink = client.resolveSameOriginReference(response.Items[index].DeepLink)
	}
	return response, nil
}

func (client *Client) FetchArtifact(ctx context.Context, request protocol.FetchArtifactRequest) (protocol.ArtifactReference, error) {
	if err := protocol.ValidateFetchArtifactRequest(request); err != nil {
		return protocol.ArtifactReference{}, err
	}
	body, _ := json.Marshal(artifactInput{HandoffID: request.HandoffID, ArtifactID: request.ArtifactID})
	responseBody, status, err := client.post(ctx, client.handoffArtifactEndpoint, body)
	if err != nil {
		return protocol.ArtifactReference{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return protocol.ArtifactReference{}, client.decodeBridgeError(responseBody, status, "AgentX Server rejected the Artifact request")
	}
	var reference protocol.ArtifactReference
	if json.Unmarshal(responseBody, &reference) != nil || protocol.ValidateArtifactReference(reference) != nil {
		return protocol.ArtifactReference{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid Artifact reference", true, false, request.HandoffID)
	}
	reference.ContentURI = client.resolveSameOriginReference(reference.ContentURI)
	reference.PreviewURI = client.resolveSameOriginReference(reference.PreviewURI)
	return reference, nil
}

// resolveSameOriginReference makes Server-owned relative links usable by a
// stdio MCP Host without attaching the Host bearer credential to the URI. An
// already-absolute HTTPS link (for example a bounded object-store URL) is
// preserved exactly as returned by the authenticated Server.
func (client *Client) resolveSameOriginReference(value string) string {
	if value == "" || client.serverOrigin == nil {
		return value
	}
	reference, err := url.Parse(value)
	if err != nil || reference.IsAbs() {
		return value
	}
	return client.serverOrigin.ResolveReference(reference).String()
}

func (client *Client) AcceptDelivery(ctx context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	return client.decideDelivery(ctx, client.handoffAcceptanceEndpoint, request)
}

func (client *Client) RejectDelivery(ctx context.Context, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	return client.decideDelivery(ctx, client.handoffRejectionEndpoint, request)
}

func (client *Client) decideDelivery(ctx context.Context, endpoint string, request protocol.DeliveryDecisionRequest) (protocol.DeliveryDecisionReceipt, error) {
	if err := protocol.ValidateDeliveryDecisionRequest(request); err != nil {
		return protocol.DeliveryDecisionReceipt{}, err
	}
	body, _ := json.Marshal(deliveryDecisionInput{
		HandoffID: request.HandoffID, MissionVersion: request.MissionVersion,
		IdempotencyKey: request.IdempotencyKey, Reason: request.Reason,
	})
	responseBody, status, err := client.post(ctx, endpoint, body)
	if err != nil {
		return protocol.DeliveryDecisionReceipt{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return protocol.DeliveryDecisionReceipt{}, client.decodeBridgeError(responseBody, status, "AgentX Server rejected the delivery decision")
	}
	var receipt protocol.DeliveryDecisionReceipt
	if json.Unmarshal(responseBody, &receipt) != nil || protocol.ValidateDeliveryDecisionReceipt(receipt) != nil {
		return protocol.DeliveryDecisionReceipt{}, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid delivery decision receipt", true, false, request.HandoffID)
	}
	return receipt, nil
}

func (client *Client) post(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, protocol.NewError(protocol.ErrorCodeInternal, "AgentX request could not be prepared", false, false, "")
	}
	request.Header.Set("Authorization", "Bearer "+client.credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "agentx-bridge-mcp/0.1")
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, protocol.NewError(protocol.ErrorCodeProviderUnavailable, "AgentX Server is unavailable", true, false, "")
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		return nil, 0, protocol.NewError(protocol.ErrorCodeInternal, "AgentX Server returned an invalid response", true, false, "")
	}
	return responseBody, response.StatusCode, nil
}

func (client *Client) decodeBridgeError(body []byte, status int, fallback string) error {
	var bridgeErr protocol.Error
	if json.Unmarshal(body, &bridgeErr) == nil && bridgeErr.Code != "" {
		bridgeErr.DeepLink = client.resolveSameOriginReference(bridgeErr.DeepLink)
		return &bridgeErr
	}
	return protocol.NewError(protocol.ErrorCodeInternal, fallback, status >= 500, status == http.StatusUnauthorized, "")
}

var _ HandoffBackend = (*Client)(nil)

func bearerCredential(value string) (string, bool) {
	parts := strings.Fields(value)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnValue = parts[1]
	}
	return returnValue, returnValue != ""
}

func decodeSingleJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func validateServerURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("AgentX Server URL must be an absolute origin")
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1") {
		return parsed, nil
	}
	return nil, errors.New("AgentX Server URL must use HTTPS except on loopback")
}

func hostSafeError(err error, fallbackCode protocol.ErrorCode, fallbackSummary string) *protocol.Error {
	var bridgeErr *protocol.Error
	if protocol.AsError(err, &bridgeErr) {
		return bridgeErr
	}
	return &protocol.Error{Code: fallbackCode, SafeSummary: fallbackSummary}
}

func statusForError(err *protocol.Error) int {
	switch err.Code {
	case protocol.ErrorCodeValidationFailed:
		return http.StatusBadRequest
	case protocol.ErrorCodeAuthenticationRequired:
		return http.StatusUnauthorized
	case protocol.ErrorCodeAuthorizationDenied:
		return http.StatusForbidden
	case protocol.ErrorCodeCapabilityNotFound:
		return http.StatusNotFound
	case protocol.ErrorCodeConflict:
		return http.StatusConflict
	case protocol.ErrorCodeRateLimited:
		return http.StatusTooManyRequests
	case protocol.ErrorCodeConnectionRequired, protocol.ErrorCodePolicyDenied, protocol.ErrorCodeBudgetExceeded:
		return http.StatusUnprocessableEntity
	case protocol.ErrorCodeProviderUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeError(writer http.ResponseWriter, status int, err error) {
	bridgeErr := hostSafeError(err, protocol.ErrorCodeInternal, "AgentX Server could not complete the request")
	writeJSON(writer, status, bridgeErr)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, fmt.Sprintf(`{"code":%q,"safeSummary":%q}`, protocol.ErrorCodeInternal, "AgentX Server could not encode the response"), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}
