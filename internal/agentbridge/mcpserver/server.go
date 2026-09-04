// Package mcpserver adapts the transport-independent Agent Bridge application
// service to the official MCP Go SDK. It owns protocol mapping only: identity,
// policy, catalog and execution remain server-side application concerns.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

const (
	implementationVersion = "0.1.0"
	serverInstructions    = "AgentX extends this Host; it is not a replacement agent or a general task planner. Keep ordinary work in the current Host when its own model, tools and granted context can complete it reliably (PASS_THROUGH). Search AgentX only when work needs a connected private capability, another responsibility owner, durable cross-session execution, or an independently verifiable external result. Follow the selected match executionMode exactly: INVOKE is short-lived and returns an InvocationReceipt; HANDOFF is persistent and returns a HandoffRef. Never silently fall back to a Runtime when Search or execution fails. Preserve HandoffRef, disclose only named required context, and use Server status before presenting, accepting or requesting revision of a delivery."
	searchDescription     = "Search capabilities visible to this signed-in Host after deciding AgentX is needed. Do not search for ordinary work the Host can reliably finish with its own model, tools and granted context, or merely to split work, switch models or create multiple agents. Use it for another responsibility owner or durable cross-session work. Search returns MATCHES_FOUND or CAPABILITY_GAP with automaticFallbackAllowed=false; never turn an empty result into an implicit Runtime call."
	invokeDescription     = "Invoke one exact short-lived AgentX binding returned by search_capabilities when no durable responsibility transfer is required. Follow executionMode exactly. The Server rechecks identity, grant, connection, policy and version and returns a durable InvocationReceipt; never silently substitute a Runtime or another binding."
	handoffDescription    = "Handoff durable work to one exact responsibility owner returned by search_capabilities when work crosses a Principal, account or permission boundary, must outlive the Host, or needs independent delivery and acceptance. Disclose only named required context, declare expected Artifacts, preserve the returned HandoffRef, and resume from Server facts."
	compactUseDescription = "Use one exact capability returned by search_capabilities or resume its durable Handoff. Follow the match executionMode: INVOKE returns an InvocationReceipt; HANDOFF returns a persistent HandoffRef. Do not guess, switch bindings or silently fall back to a Runtime. Persistent work must be resumed, reviewed, accepted or revised from Server facts."
)

type ToolProfile string

const (
	ToolProfileFull    ToolProfile = "full"
	ToolProfileCompact ToolProfile = "compact"
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

type Config struct {
	Caller  protocol.CallerContext
	Search  SearchBackend
	Invoke  InvokeBackend
	Handoff HandoffBackend
	Profile ToolProfile
}

type InvokeToolInput struct {
	BindingID      string         `json:"bindingId" jsonschema:"exact binding ID returned by search_capabilities"`
	BindingVersion string         `json:"bindingVersion" jsonschema:"exact immutable binding version returned by search_capabilities"`
	IdempotencyKey string         `json:"idempotencyKey" jsonschema:"stable key reused only for the same invocation request"`
	Input          map[string]any `json:"input" jsonschema:"capability-specific bounded JSON object"`
}

// InvokeToolResult preserves the InvocationReceipt wire field names while
// projecting the bounded JSON output as an object. json.RawMessage is a byte
// slice to Go schema reflection and would otherwise be advertised by MCP as an
// array even though its JSON wire value is an object.
type InvokeToolResult struct {
	ContractVersion string                    `json:"contractVersion"`
	InvocationID    string                    `json:"invocationId"`
	BindingID       string                    `json:"bindingId"`
	BindingVersion  string                    `json:"bindingVersion"`
	Status          protocol.InvocationStatus `json:"status"`
	ArtifactRefs    []string                  `json:"artifactRefs,omitempty"`
	OutputMediaType string                    `json:"outputMediaType,omitempty"`
	Output          map[string]any            `json:"output,omitempty"`
	SafeSummary     string                    `json:"safeSummary"`
	TraceID         string                    `json:"traceId"`
	StartedAt       time.Time                 `json:"startedAt"`
	FinishedAt      *time.Time                `json:"finishedAt,omitempty"`
	DurationMillis  int64                     `json:"durationMillis"`
	InputBytes      int64                     `json:"inputBytes"`
	OutputBytes     int64                     `json:"outputBytes"`
	CostMinor       *int64                    `json:"costMinor,omitempty"`
	Currency        string                    `json:"currency,omitempty"`
}

type SearchToolInput struct {
	Query                 string               `json:"query" jsonschema:"the work capability or result the user needs"`
	ExpectedArtifactTypes []string             `json:"expectedArtifactTypes,omitempty" jsonschema:"optional MIME types the result must provide"`
	MaximumSensitivity    protocol.Sensitivity `json:"maximumSensitivity,omitempty" jsonschema:"highest data sensitivity needed: PUBLIC, INTERNAL, or CONFIDENTIAL"`
	Mode                  protocol.RoutingMode `json:"mode,omitempty" jsonschema:"routing preference: ECONOMY, BALANCED, EFFECT, or FAST"`
	BudgetMaximumMinor    *int64               `json:"budgetMaximumMinor,omitempty" jsonschema:"maximum budget in minor currency units"`
	BudgetCurrency        string               `json:"budgetCurrency,omitempty" jsonschema:"currency code used with budgetMaximumMinor"`
	Deadline              *time.Time           `json:"deadline,omitempty" jsonschema:"optional RFC3339 completion deadline"`
	Limit                 uint32               `json:"limit,omitempty" jsonschema:"maximum matches, from 1 to 20"`
}

type HandoffToolInput struct {
	BindingID         string                         `json:"bindingId" jsonschema:"exact HANDOFF binding ID returned by search_capabilities"`
	BindingVersion    string                         `json:"bindingVersion" jsonschema:"exact immutable binding version returned by search_capabilities"`
	IdempotencyKey    string                         `json:"idempotencyKey" jsonschema:"stable key reused only for the same handoff request"`
	Goal              string                         `json:"goal" jsonschema:"the complete result the responsibility owner must deliver"`
	Context           []protocol.ContextReference    `json:"context,omitempty" jsonschema:"named references disclosed to the selected provider; P0 requires explicit disclosure support"`
	ExpectedArtifacts []protocol.ArtifactExpectation `json:"expectedArtifacts" jsonschema:"named Artifact contracts the delivery must satisfy"`
	Deadline          *time.Time                     `json:"deadline,omitempty" jsonschema:"optional RFC3339 completion deadline"`
}

type HandoffStatusToolInput struct {
	HandoffID         string `json:"handoffId" jsonschema:"opaque Handoff ID returned by handoff_work"`
	KnownUpdateCursor string `json:"knownUpdateCursor,omitempty" jsonschema:"optional opaque update cursor returned by the last status call; matching cursors make changed false"`
}

type HandoffListToolInput struct {
	Limit uint32 `json:"limit,omitempty" jsonschema:"maximum recent handoffs to return, from 1 to 20; defaults to 10"`
}

type FetchArtifactToolInput struct {
	HandoffID  string `json:"handoffId" jsonschema:"opaque Handoff ID returned by handoff_work"`
	ArtifactID string `json:"artifactId" jsonschema:"Artifact ID returned by get_handoff_status"`
}

type DeliveryDecisionToolInput struct {
	HandoffID      string `json:"handoffId" jsonschema:"opaque Handoff ID returned by handoff_work"`
	MissionVersion uint64 `json:"missionVersion" jsonschema:"exact Mission version observed before making this decision"`
	IdempotencyKey string `json:"idempotencyKey" jsonschema:"stable key reused only for the same delivery decision"`
	Reason         string `json:"reason,omitempty" jsonschema:"optional bounded acceptance or revision reason"`
}

type CompactOperation string

const (
	CompactOperationInvoke         CompactOperation = "INVOKE"
	CompactOperationHandoff        CompactOperation = "HANDOFF"
	CompactOperationListHandoffs   CompactOperation = "LIST_HANDOFFS"
	CompactOperationGetStatus      CompactOperation = "GET_STATUS"
	CompactOperationFetchArtifact  CompactOperation = "FETCH_ARTIFACT"
	CompactOperationAcceptDelivery CompactOperation = "ACCEPT_DELIVERY"
	CompactOperationRejectDelivery CompactOperation = "REJECT_DELIVERY"
)

type CompactToolInput struct {
	Operation         CompactOperation               `json:"operation" jsonschema:"explicit action: INVOKE, HANDOFF, LIST_HANDOFFS, GET_STATUS, FETCH_ARTIFACT, ACCEPT_DELIVERY, or REJECT_DELIVERY"`
	BindingID         string                         `json:"bindingId,omitempty" jsonschema:"exact binding ID returned by search_capabilities"`
	BindingVersion    string                         `json:"bindingVersion,omitempty" jsonschema:"exact immutable binding version returned by search_capabilities"`
	IdempotencyKey    string                         `json:"idempotencyKey,omitempty" jsonschema:"stable key reused only for the identical side-effect request"`
	Input             map[string]any                 `json:"input,omitempty" jsonschema:"bounded JSON object used only by INVOKE"`
	Goal              string                         `json:"goal,omitempty" jsonschema:"complete result used only by HANDOFF"`
	Context           []protocol.ContextReference    `json:"context,omitempty" jsonschema:"named disclosed references used only by HANDOFF"`
	ExpectedArtifacts []protocol.ArtifactExpectation `json:"expectedArtifacts,omitempty" jsonschema:"named Artifact contracts used only by HANDOFF"`
	Deadline          *time.Time                     `json:"deadline,omitempty" jsonschema:"optional HANDOFF completion deadline"`
	Limit             uint32                         `json:"limit,omitempty" jsonschema:"bounded result count used only by LIST_HANDOFFS"`
	HandoffID         string                         `json:"handoffId,omitempty" jsonschema:"opaque Handoff ID used by resume and decision operations"`
	KnownUpdateCursor string                         `json:"knownUpdateCursor,omitempty" jsonschema:"optional opaque cursor used only by GET_STATUS to suppress duplicate presentation"`
	ArtifactID        string                         `json:"artifactId,omitempty" jsonschema:"Artifact ID used only by FETCH_ARTIFACT"`
	MissionVersion    uint64                         `json:"missionVersion,omitempty" jsonschema:"exact Mission version used only by delivery decisions"`
	Reason            string                         `json:"reason,omitempty" jsonschema:"bounded reason used only by delivery decisions"`
}

type CompactResultKind string

const (
	CompactResultInvocation  CompactResultKind = "INVOCATION_RECEIPT"
	CompactResultHandoff     CompactResultKind = "HANDOFF_REF"
	CompactResultHandoffList CompactResultKind = "HANDOFF_LIST"
	CompactResultStatus      CompactResultKind = "HANDOFF_STATUS"
	CompactResultArtifact    CompactResultKind = "ARTIFACT_REFERENCE"
	CompactResultDecision    CompactResultKind = "DELIVERY_DECISION_RECEIPT"
)

type CompactToolResult struct {
	Kind       CompactResultKind                 `json:"kind"`
	Invocation *InvokeToolResult                 `json:"invocation,omitempty"`
	Handoff    *protocol.HandoffRef              `json:"handoff,omitempty"`
	Handoffs   *protocol.ListHandoffsResponse    `json:"handoffs,omitempty"`
	Status     *protocol.HandoffStatusView       `json:"status,omitempty"`
	Artifact   *protocol.ArtifactReference       `json:"artifact,omitempty"`
	Decision   *protocol.DeliveryDecisionReceipt `json:"decision,omitempty"`
}

func New(config Config) (*mcp.Server, error) {
	if config.Search == nil {
		return nil, errors.New("Agent Bridge MCP server requires a search backend")
	}
	if err := protocol.ValidateCallerContext(config.Caller); err != nil {
		return nil, errors.New("Agent Bridge MCP server requires authenticated Host identity")
	}
	if config.Profile == "" {
		config.Profile = ToolProfileFull
	}
	if config.Profile != ToolProfileFull && config.Profile != ToolProfileCompact {
		return nil, errors.New("Agent Bridge MCP tool profile is invalid")
	}
	if config.Profile == ToolProfileCompact && (config.Invoke == nil || config.Handoff == nil) {
		return nil, errors.New("Compact Agent Bridge MCP requires invoke and handoff backends")
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agentx-bridge", Version: implementationVersion},
		&mcp.ServerOptions{Instructions: serverInstructions},
	)
	readOnly, closedWorld := true, false
	mcp.AddTool(server, &mcp.Tool{
		Name:        string(protocol.ToolSearchCapabilities),
		Title:       "Search AgentX capabilities",
		Description: searchDescription,
		Annotations: &mcp.ToolAnnotations{
			Title: "Search AgentX capabilities", ReadOnlyHint: readOnly, IdempotentHint: true, OpenWorldHint: &closedWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input SearchToolInput) (*mcp.CallToolResult, protocol.SearchCapabilitiesResponse, error) {
		if input.MaximumSensitivity == "" {
			input.MaximumSensitivity = protocol.SensitivityInternal
		}
		if input.Mode == "" {
			input.Mode = protocol.RoutingModeBalanced
		}
		response, err := config.Search.Search(ctx, protocol.SearchCapabilitiesRequest{
			ContractVersion: protocol.ContractVersion, Caller: config.Caller, Query: input.Query,
			ExpectedArtifactTypes: input.ExpectedArtifactTypes, MaximumSensitivity: input.MaximumSensitivity, Mode: input.Mode,
			BudgetMaximumMinor: input.BudgetMaximumMinor, BudgetCurrency: input.BudgetCurrency, Deadline: input.Deadline, Limit: input.Limit,
		})
		if err != nil {
			return nil, protocol.SearchCapabilitiesResponse{}, safeToolError(err)
		}
		if err := protocol.ValidateSearchCapabilitiesResponse(response); err != nil {
			return nil, protocol.SearchCapabilitiesResponse{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability search returned an invalid disposition", true, false, "")
		}
		return nil, response, nil
	})
	if config.Profile == ToolProfileCompact {
		addCompactUseTool(server, config)
		return server, nil
	}
	if config.Invoke != nil {
		readOnly, destructive, openWorld := false, false, true
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(protocol.ToolInvokeCapability),
			Title:       "Invoke an AgentX capability",
			Description: invokeDescription,
			Annotations: &mcp.ToolAnnotations{
				Title: "Invoke an AgentX capability", ReadOnlyHint: readOnly, DestructiveHint: &destructive,
				IdempotentHint: true, OpenWorldHint: &openWorld,
			},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input InvokeToolInput) (*mcp.CallToolResult, InvokeToolResult, error) {
			rawInput, err := json.Marshal(input.Input)
			if err != nil {
				return nil, InvokeToolResult{}, protocol.NewError(protocol.ErrorCodeValidationFailed, "Capability input is invalid", false, false, "")
			}
			receipt, err := config.Invoke.Invoke(ctx, protocol.InvokeCapabilityRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller,
				BindingID: input.BindingID, BindingVersion: input.BindingVersion,
				IdempotencyKey: input.IdempotencyKey, Input: rawInput,
			})
			if err != nil {
				return nil, InvokeToolResult{}, safeToolError(err)
			}
			if err := protocol.ValidateInvocationReceipt(receipt); err != nil {
				return nil, InvokeToolResult{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability returned an invalid Invocation Receipt", true, false, "")
			}
			projected, err := projectInvocationReceipt(receipt)
			if err != nil {
				return nil, InvokeToolResult{}, err
			}
			return nil, projected, nil
		})
	}
	if config.Handoff != nil {
		readOnly, destructive, openWorld := false, false, true
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(protocol.ToolHandoffWork),
			Title:       "Handoff work through AgentX",
			Description: handoffDescription,
			Annotations: &mcp.ToolAnnotations{
				Title: "Handoff work through AgentX", ReadOnlyHint: readOnly, DestructiveHint: &destructive,
				IdempotentHint: true, OpenWorldHint: &openWorld,
			},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input HandoffToolInput) (*mcp.CallToolResult, protocol.HandoffRef, error) {
			reference, err := config.Handoff.Handoff(ctx, protocol.HandoffWorkRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, BindingID: input.BindingID,
				BindingVersion: input.BindingVersion, IdempotencyKey: input.IdempotencyKey, Goal: input.Goal,
				Context: input.Context, ExpectedArtifacts: input.ExpectedArtifacts, Deadline: input.Deadline,
			})
			if err != nil {
				return nil, protocol.HandoffRef{}, safeToolError(err)
			}
			return nil, reference, nil
		})
		readOnly = true
		closedWorld := false
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(protocol.ToolListHandoffs),
			Title:       "List recent AgentX handoffs",
			Description: "Find this signed-in user's recent caller-scoped AgentX handoffs across authorized Host installations. Use it when the user asks to continue or view a recent AgentX task but the current Host no longer has the HandoffRef. The bounded list is read-only; call get_handoff_status before presenting or deciding on one item.",
			Annotations: &mcp.ToolAnnotations{
				Title: "List recent AgentX handoffs", ReadOnlyHint: readOnly, IdempotentHint: true, OpenWorldHint: &closedWorld,
			},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input HandoffListToolInput) (*mcp.CallToolResult, protocol.ListHandoffsResponse, error) {
			response, err := config.Handoff.List(ctx, protocol.ListHandoffsRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, Limit: input.Limit,
			})
			if err != nil {
				return nil, protocol.ListHandoffsResponse{}, safeToolError(err)
			}
			return nil, response, nil
		})
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(protocol.ToolGetHandoffStatus),
			Title:       "Get AgentX handoff status",
			Description: "Resume a caller-bound AgentX handoff from Server facts. Returns the current Mission version, bounded Need You items, current Artifact and Verification metadata, Recovery or accepted Outcome references, and the exact next user action. Refresh this view before accepting or requesting revision.",
			Annotations: &mcp.ToolAnnotations{
				Title: "Get AgentX handoff status", ReadOnlyHint: readOnly, IdempotentHint: true, OpenWorldHint: &closedWorld,
			},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input HandoffStatusToolInput) (*mcp.CallToolResult, protocol.HandoffStatusView, error) {
			view, err := config.Handoff.Status(ctx, protocol.GetHandoffStatusRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, HandoffID: input.HandoffID,
				KnownUpdateCursor: input.KnownUpdateCursor,
			})
			if err != nil {
				return nil, protocol.HandoffStatusView{}, safeToolError(err)
			}
			return nil, view, nil
		})
		mcp.AddTool(server, &mcp.Tool{
			Name:        string(protocol.ToolFetchArtifact),
			Title:       "Fetch an AgentX Artifact reference",
			Description: "Fetch immutable metadata and an authorized user-facing content reference for an Artifact that belongs to this caller's handoff.",
			Annotations: &mcp.ToolAnnotations{
				Title: "Fetch an AgentX Artifact reference", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld,
			},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, input FetchArtifactToolInput) (*mcp.CallToolResult, protocol.ArtifactReference, error) {
			reference, err := config.Handoff.FetchArtifact(ctx, protocol.FetchArtifactRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller,
				HandoffID: input.HandoffID, ArtifactID: input.ArtifactID,
			})
			if err != nil {
				return nil, protocol.ArtifactReference{}, safeToolError(err)
			}
			return nil, reference, nil
		})
		addDeliveryDecisionTool(server, config, protocol.ToolAcceptDelivery, "Accept AgentX delivery",
			"Accept a verified handoff delivery using the exact Mission version observed by the user. Acceptance is a durable user decision, not a Runtime completion claim.", false)
		addDeliveryDecisionTool(server, config, protocol.ToolRejectDelivery, "Request AgentX delivery revision",
			"Reject the current delivery and request revision. AgentX preserves existing Artifacts and enters Gap/Recovery instead of silently restarting from zero.", true)
	}
	return server, nil
}

func addCompactUseTool(server *mcp.Server, config Config) {
	readOnly, destructive, openWorld := false, false, true
	mcp.AddTool(server, &mcp.Tool{
		Name: string(protocol.ToolUseCapability), Title: "Use or resume an AgentX capability",
		Description: compactUseDescription,
		Annotations: &mcp.ToolAnnotations{
			Title: "Use or resume an AgentX capability", ReadOnlyHint: readOnly, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input CompactToolInput) (*mcp.CallToolResult, CompactToolResult, error) {
		if err := validateCompactToolInput(input); err != nil {
			return nil, CompactToolResult{}, err
		}
		switch input.Operation {
		case CompactOperationInvoke:
			rawInput, err := json.Marshal(input.Input)
			if err != nil {
				return nil, CompactToolResult{}, protocol.NewError(protocol.ErrorCodeValidationFailed, "Capability input is invalid", false, false, "")
			}
			receipt, err := config.Invoke.Invoke(ctx, protocol.InvokeCapabilityRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller,
				BindingID: input.BindingID, BindingVersion: input.BindingVersion,
				IdempotencyKey: input.IdempotencyKey, Input: rawInput,
			})
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			if err := protocol.ValidateInvocationReceipt(receipt); err != nil {
				return nil, CompactToolResult{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability returned an invalid Invocation Receipt", true, false, "")
			}
			projected, err := projectInvocationReceipt(receipt)
			if err != nil {
				return nil, CompactToolResult{}, err
			}
			return nil, CompactToolResult{Kind: CompactResultInvocation, Invocation: &projected}, nil
		case CompactOperationHandoff:
			reference, err := config.Handoff.Handoff(ctx, protocol.HandoffWorkRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller,
				BindingID: input.BindingID, BindingVersion: input.BindingVersion,
				IdempotencyKey: input.IdempotencyKey, Goal: input.Goal, Context: input.Context,
				ExpectedArtifacts: input.ExpectedArtifacts, Deadline: input.Deadline,
			})
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			return nil, CompactToolResult{Kind: CompactResultHandoff, Handoff: &reference}, nil
		case CompactOperationListHandoffs:
			response, err := config.Handoff.List(ctx, protocol.ListHandoffsRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, Limit: input.Limit,
			})
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			return nil, CompactToolResult{Kind: CompactResultHandoffList, Handoffs: &response}, nil
		case CompactOperationGetStatus:
			view, err := config.Handoff.Status(ctx, protocol.GetHandoffStatusRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, HandoffID: input.HandoffID,
				KnownUpdateCursor: input.KnownUpdateCursor,
			})
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			return nil, CompactToolResult{Kind: CompactResultStatus, Status: &view}, nil
		case CompactOperationFetchArtifact:
			reference, err := config.Handoff.FetchArtifact(ctx, protocol.FetchArtifactRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller,
				HandoffID: input.HandoffID, ArtifactID: input.ArtifactID,
			})
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			return nil, CompactToolResult{Kind: CompactResultArtifact, Artifact: &reference}, nil
		case CompactOperationAcceptDelivery, CompactOperationRejectDelivery:
			request := protocol.DeliveryDecisionRequest{
				ContractVersion: protocol.ContractVersion, Caller: config.Caller, HandoffID: input.HandoffID,
				MissionVersion: input.MissionVersion, IdempotencyKey: input.IdempotencyKey, Reason: input.Reason,
			}
			var receipt protocol.DeliveryDecisionReceipt
			var err error
			if input.Operation == CompactOperationRejectDelivery {
				receipt, err = config.Handoff.RejectDelivery(ctx, request)
			} else {
				receipt, err = config.Handoff.AcceptDelivery(ctx, request)
			}
			if err != nil {
				return nil, CompactToolResult{}, safeToolError(err)
			}
			return nil, CompactToolResult{Kind: CompactResultDecision, Decision: &receipt}, nil
		default:
			return nil, CompactToolResult{}, protocol.NewError(protocol.ErrorCodeValidationFailed, "Compact AgentX operation is invalid", false, false, "")
		}
	})
}

func validateCompactToolInput(input CompactToolInput) error {
	invalid := func() error {
		return protocol.NewError(protocol.ErrorCodeValidationFailed, "Compact AgentX operation contains missing or unrelated fields", false, false, "")
	}
	hasStartFields := input.BindingID != "" || input.BindingVersion != "" || input.Input != nil || input.Goal != "" ||
		len(input.Context) != 0 || len(input.ExpectedArtifacts) != 0 || input.Deadline != nil
	hasResumeFields := input.HandoffID != "" || input.KnownUpdateCursor != "" || input.ArtifactID != "" || input.MissionVersion != 0 || input.Reason != ""
	switch input.Operation {
	case CompactOperationInvoke:
		if input.Limit != 0 || input.BindingID == "" || input.BindingVersion == "" || input.IdempotencyKey == "" || input.Input == nil ||
			input.Goal != "" || len(input.Context) != 0 || len(input.ExpectedArtifacts) != 0 || input.Deadline != nil || hasResumeFields {
			return invalid()
		}
	case CompactOperationHandoff:
		if input.Limit != 0 || input.BindingID == "" || input.BindingVersion == "" || input.IdempotencyKey == "" || input.Goal == "" ||
			len(input.ExpectedArtifacts) == 0 || input.Input != nil || hasResumeFields {
			return invalid()
		}
	case CompactOperationListHandoffs:
		if input.Limit > protocol.MaxHandoffListLimit || input.IdempotencyKey != "" || hasStartFields || hasResumeFields {
			return invalid()
		}
	case CompactOperationGetStatus:
		if !hasResumeFields || input.HandoffID == "" || input.Limit != 0 || input.ArtifactID != "" || input.MissionVersion != 0 || input.Reason != "" || input.IdempotencyKey != "" || hasStartFields {
			return invalid()
		}
	case CompactOperationFetchArtifact:
		if input.HandoffID == "" || input.Limit != 0 || input.KnownUpdateCursor != "" || input.ArtifactID == "" || input.MissionVersion != 0 || input.Reason != "" || input.IdempotencyKey != "" || hasStartFields {
			return invalid()
		}
	case CompactOperationAcceptDelivery, CompactOperationRejectDelivery:
		if input.HandoffID == "" || input.Limit != 0 || input.KnownUpdateCursor != "" || input.MissionVersion == 0 || input.IdempotencyKey == "" || input.ArtifactID != "" || hasStartFields {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

func addDeliveryDecisionTool(
	server *mcp.Server,
	config Config,
	name protocol.ToolName,
	title string,
	description string,
	reject bool,
) {
	readOnly, destructive, openWorld := false, false, false
	mcp.AddTool(server, &mcp.Tool{
		Name: string(name), Title: title, Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title: title, ReadOnlyHint: readOnly, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input DeliveryDecisionToolInput) (*mcp.CallToolResult, protocol.DeliveryDecisionReceipt, error) {
		request := protocol.DeliveryDecisionRequest{
			ContractVersion: protocol.ContractVersion, Caller: config.Caller, HandoffID: input.HandoffID,
			MissionVersion: input.MissionVersion, IdempotencyKey: input.IdempotencyKey, Reason: input.Reason,
		}
		var receipt protocol.DeliveryDecisionReceipt
		var err error
		if reject {
			receipt, err = config.Handoff.RejectDelivery(ctx, request)
		} else {
			receipt, err = config.Handoff.AcceptDelivery(ctx, request)
		}
		if err != nil {
			return nil, protocol.DeliveryDecisionReceipt{}, safeToolError(err)
		}
		return nil, receipt, nil
	})
}

func projectInvocationReceipt(receipt protocol.InvocationReceipt) (InvokeToolResult, error) {
	var output map[string]any
	if len(receipt.Output) > 0 {
		if err := json.Unmarshal(receipt.Output, &output); err != nil || output == nil {
			return InvokeToolResult{}, protocol.NewError(protocol.ErrorCodeInternal, "Capability output is not an MCP-compatible JSON object", false, false, receipt.InvocationID)
		}
	}
	return InvokeToolResult{
		ContractVersion: receipt.ContractVersion, InvocationID: receipt.InvocationID,
		BindingID: receipt.BindingID, BindingVersion: receipt.BindingVersion, Status: receipt.Status,
		ArtifactRefs: receipt.ArtifactRefs, OutputMediaType: receipt.OutputMediaType, Output: output,
		SafeSummary: receipt.SafeSummary, TraceID: receipt.TraceID,
		StartedAt: receipt.StartedAt, FinishedAt: receipt.FinishedAt, DurationMillis: receipt.DurationMillis,
		InputBytes: receipt.InputBytes, OutputBytes: receipt.OutputBytes, CostMinor: receipt.CostMinor, Currency: receipt.Currency,
	}, nil
}

func safeToolError(err error) error {
	var bridgeError *protocol.Error
	if protocol.AsError(err, &bridgeError) {
		return bridgeError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return protocol.NewError(protocol.ErrorCodeProviderUnavailable, "AgentX operation was canceled or timed out", true, false, "")
	}
	return protocol.NewError(protocol.ErrorCodeInternal, "AgentX operation is temporarily unavailable", true, false, "")
}
