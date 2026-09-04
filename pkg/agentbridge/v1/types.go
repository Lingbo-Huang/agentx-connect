// Package agentbridge defines the versioned Host-facing contract used by
// Codex, Claude, Seal and other Agent hosts. It does not define local native
// file/workspace actions; those belong to pkg/nativebridge/v1.
package agentbridge

import (
	"encoding/json"
	"time"
)

const ContractVersion = "agent-bridge.agentx.dev/v1"

const (
	MaxQueryBytes        = 4_096
	MaxInvokeInputBytes  = 1 << 20
	MaxInvokeOutputBytes = 64 << 10
	MaxContextSizeBytes  = 100 << 20
	MaxHandoffListLimit  = 20
)

type ToolName string

const (
	ToolSearchCapabilities ToolName = "search_capabilities"
	ToolInvokeCapability   ToolName = "invoke_capability"
	ToolHandoffWork        ToolName = "handoff_work"
	ToolListHandoffs       ToolName = "list_handoffs"
	ToolGetHandoffStatus   ToolName = "get_handoff_status"
	ToolFetchArtifact      ToolName = "fetch_artifact"
	ToolAcceptDelivery     ToolName = "accept_delivery"
	ToolRejectDelivery     ToolName = "reject_delivery"
	ToolUseCapability      ToolName = "use_capability"
)

var toolSurface = []ToolName{
	ToolSearchCapabilities,
	ToolInvokeCapability,
	ToolHandoffWork,
	ToolListHandoffs,
	ToolGetHandoffStatus,
	ToolFetchArtifact,
	ToolAcceptDelivery,
	ToolRejectDelivery,
}

func ToolSurface() []ToolName { return append([]ToolName(nil), toolSurface...) }

var compactToolSurface = []ToolName{ToolSearchCapabilities, ToolUseCapability}

func CompactToolSurface() []ToolName { return append([]ToolName(nil), compactToolSurface...) }

type HostKind string

const (
	HostKindCodex  HostKind = "CODEX"
	HostKindClaude HostKind = "CLAUDE"
	// HostKindSeal covers Seal and explicitly compatible OpenClaw installations.
	// It does not make OpenClaw an AgentX Runtime or completion authority.
	HostKindSeal      HostKind = "SEAL"
	HostKindCursor    HostKind = "CURSOR"
	HostKindWorkBuddy HostKind = "WORKBUDDY"
	HostKindTrae      HostKind = "TRAE"
	// HostKindWeb is the AgentX first-party browser intake. It is a source
	// surface, not a Device Authorization target or a user-owned Runtime.
	HostKindWeb   HostKind = "WEB"
	HostKindOther HostKind = "OTHER"
)

type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "PUBLIC"
	SensitivityInternal     Sensitivity = "INTERNAL"
	SensitivityConfidential Sensitivity = "CONFIDENTIAL"
)

type RoutingMode string

const (
	RoutingModeEconomy  RoutingMode = "ECONOMY"
	RoutingModeBalanced RoutingMode = "BALANCED"
	RoutingModeEffect   RoutingMode = "EFFECT"
	RoutingModeFast     RoutingMode = "FAST"
)

type ExecutionMode string

const (
	ExecutionModeInvoke  ExecutionMode = "INVOKE"
	ExecutionModeHandoff ExecutionMode = "HANDOFF"
)

type ConnectionState string

const (
	ConnectionStateReady       ConnectionState = "READY"
	ConnectionStateRequired    ConnectionState = "REQUIRED"
	ConnectionStateUnavailable ConnectionState = "UNAVAILABLE"
)

type GrantState string

const (
	GrantStateGranted  GrantState = "GRANTED"
	GrantStateRequired GrantState = "REQUIRED"
	GrantStateDenied   GrantState = "DENIED"
)

type CallerContext struct {
	PrincipalID        string   `json:"principalId"`
	SpaceID            string   `json:"spaceId"`
	HostInstallationID string   `json:"hostInstallationId"`
	HostKind           HostKind `json:"hostKind"`
}

type SearchCapabilitiesRequest struct {
	ContractVersion       string        `json:"contractVersion"`
	Caller                CallerContext `json:"caller"`
	Query                 string        `json:"query"`
	ExpectedArtifactTypes []string      `json:"expectedArtifactTypes,omitempty"`
	MaximumSensitivity    Sensitivity   `json:"maximumSensitivity"`
	Mode                  RoutingMode   `json:"mode"`
	BudgetMaximumMinor    *int64        `json:"budgetMaximumMinor,omitempty"`
	BudgetCurrency        string        `json:"budgetCurrency,omitempty"`
	Deadline              *time.Time    `json:"deadline,omitempty"`
	Limit                 uint32        `json:"limit,omitempty"`
}

type ProviderSummary struct {
	ProviderID  string `json:"providerId"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
}

type FulfillmentEvidenceLevel string

const (
	FulfillmentEvidenceColdStart FulfillmentEvidenceLevel = "COLD_START"
	FulfillmentEvidenceObserved  FulfillmentEvidenceLevel = "OBSERVED"
	FulfillmentEvidenceReliable  FulfillmentEvidenceLevel = "RELIABLE"
)

// FulfillmentEvidence is an aggregate, privacy-safe projection. It never
// exposes another customer's goal, Artifact, identity or free-form feedback.
type FulfillmentEvidence struct {
	Level                    FulfillmentEvidenceLevel `json:"level"`
	SampleCount              uint32                   `json:"sampleCount"`
	AcceptedCount            uint32                   `json:"acceptedCount"`
	RevisionCount            uint32                   `json:"revisionCount"`
	FailureCount             uint32                   `json:"failureCount"`
	FirstPassAcceptanceBasis uint32                   `json:"firstPassAcceptanceBasis"`
	AverageDurationMillis    int64                    `json:"averageDurationMillis,omitempty"`
}

type CapabilityMatch struct {
	CapabilityID            string               `json:"capabilityId"`
	ServiceID               string               `json:"serviceId"`
	ServiceVersionID        string               `json:"serviceVersionId"`
	DisplayName             string               `json:"displayName"`
	Description             string               `json:"description"`
	CapabilityCodes         []string             `json:"capabilityCodes,omitempty"`
	BindingID               string               `json:"bindingId"`
	BindingVersion          string               `json:"bindingVersion"`
	ExecutionMode           ExecutionMode        `json:"executionMode"`
	Provider                ProviderSummary      `json:"provider"`
	ArtifactTypes           []string             `json:"artifactTypes"`
	VerifierKinds           []string             `json:"verifierKinds"`
	ConnectionState         ConnectionState      `json:"connectionState"`
	GrantState              GrantState           `json:"grantState"`
	EstimatedDurationMillis int64                `json:"estimatedDurationMillis"`
	EstimatedCostMinor      *int64               `json:"estimatedCostMinor,omitempty"`
	Currency                string               `json:"currency,omitempty"`
	Reason                  string               `json:"reason,omitempty"`
	FulfillmentEvidence     *FulfillmentEvidence `json:"fulfillmentEvidence,omitempty"`
}

type SearchDisposition string

const (
	SearchDispositionMatchesFound  SearchDisposition = "MATCHES_FOUND"
	SearchDispositionCapabilityGap SearchDisposition = "CAPABILITY_GAP"
)

type SearchNextAction string

const (
	SearchNextActionSelectExactMatch   SearchNextAction = "SELECT_EXACT_MATCH"
	SearchNextActionAskUserOrReportGap SearchNextAction = "ASK_USER_OR_REPORT_GAP"
)

type SearchCapabilitiesResponse struct {
	ContractVersion          string            `json:"contractVersion"`
	Disposition              SearchDisposition `json:"disposition"`
	NextAction               SearchNextAction  `json:"nextAction"`
	AutomaticFallbackAllowed bool              `json:"automaticFallbackAllowed"`
	SafeSummary              string            `json:"safeSummary"`
	Matches                  []CapabilityMatch `json:"matches"`
}

type InvokeCapabilityRequest struct {
	ContractVersion string          `json:"contractVersion"`
	Caller          CallerContext   `json:"caller"`
	BindingID       string          `json:"bindingId"`
	BindingVersion  string          `json:"bindingVersion"`
	IdempotencyKey  string          `json:"idempotencyKey"`
	Input           json.RawMessage `json:"input"`
}

type InvocationStatus string

const (
	InvocationStatusSucceeded InvocationStatus = "SUCCEEDED"
	InvocationStatusFailed    InvocationStatus = "FAILED"
	InvocationStatusUnknown   InvocationStatus = "UNKNOWN"
)

type InvocationReceipt struct {
	ContractVersion string           `json:"contractVersion"`
	InvocationID    string           `json:"invocationId"`
	BindingID       string           `json:"bindingId"`
	BindingVersion  string           `json:"bindingVersion"`
	Status          InvocationStatus `json:"status"`
	ArtifactRefs    []string         `json:"artifactRefs,omitempty"`
	OutputMediaType string           `json:"outputMediaType,omitempty"`
	Output          json.RawMessage  `json:"output,omitempty"`
	SafeSummary     string           `json:"safeSummary"`
	TraceID         string           `json:"traceId"`
	StartedAt       time.Time        `json:"startedAt"`
	FinishedAt      *time.Time       `json:"finishedAt,omitempty"`
	DurationMillis  int64            `json:"durationMillis"`
	InputBytes      int64            `json:"inputBytes"`
	OutputBytes     int64            `json:"outputBytes"`
	CostMinor       *int64           `json:"costMinor,omitempty"`
	Currency        string           `json:"currency,omitempty"`
}

type ContextReference struct {
	Name        string      `json:"name"`
	URI         string      `json:"uri"`
	MediaType   string      `json:"mediaType"`
	SizeBytes   int64       `json:"sizeBytes"`
	ContentHash string      `json:"contentHash"`
	Sensitivity Sensitivity `json:"sensitivity"`
}

type ArtifactExpectation struct {
	LogicalName string `json:"logicalName"`
	MediaType   string `json:"mediaType"`
}

type HandoffWorkRequest struct {
	ContractVersion   string                `json:"contractVersion"`
	Caller            CallerContext         `json:"caller"`
	BindingID         string                `json:"bindingId"`
	BindingVersion    string                `json:"bindingVersion"`
	IdempotencyKey    string                `json:"idempotencyKey"`
	Goal              string                `json:"goal"`
	Context           []ContextReference    `json:"context,omitempty"`
	ExpectedArtifacts []ArtifactExpectation `json:"expectedArtifacts"`
	Deadline          *time.Time            `json:"deadline,omitempty"`
}

type HandoffStatus string

const (
	HandoffStatusQueued     HandoffStatus = "QUEUED"
	HandoffStatusInProgress HandoffStatus = "IN_PROGRESS"
	HandoffStatusNeedsUser  HandoffStatus = "NEEDS_USER"
	HandoffStatusDelivered  HandoffStatus = "DELIVERED"
	HandoffStatusAccepted   HandoffStatus = "ACCEPTED"
	HandoffStatusRecovery   HandoffStatus = "RECOVERY"
	HandoffStatusRejected   HandoffStatus = "REJECTED"
	HandoffStatusCanceled   HandoffStatus = "CANCELED"
)

type HandoffRef struct {
	ContractVersion   string                `json:"contractVersion"`
	HandoffID         string                `json:"handoffId"`
	MissionID         string                `json:"missionId"`
	Status            HandoffStatus         `json:"status"`
	NeedYouCount      uint32                `json:"needYouCount"`
	ExpectedArtifacts []ArtifactExpectation `json:"expectedArtifacts"`
	Provider          ProviderSummary       `json:"provider"`
	CreatedAt         time.Time             `json:"createdAt"`
	DeepLink          string                `json:"deepLink"`
	PortalURL         string                `json:"portalUrl"`
	PollAfterMillis   int64                 `json:"pollAfterMillis,omitempty"`
}

type GetHandoffStatusRequest struct {
	ContractVersion   string        `json:"contractVersion"`
	Caller            CallerContext `json:"caller"`
	HandoffID         string        `json:"handoffId"`
	KnownUpdateCursor string        `json:"knownUpdateCursor,omitempty"`
}

type ListHandoffsRequest struct {
	ContractVersion string        `json:"contractVersion"`
	Caller          CallerContext `json:"caller"`
	Limit           uint32        `json:"limit,omitempty"`
}

// ResumeDirective tells a Host what the user can usefully do next. It is a
// projection of Mission facts, never a second workflow state machine.
type ResumeDirective string

const (
	ResumeDirectiveWait           ResumeDirective = "WAIT"
	ResumeDirectiveResolveNeedYou ResumeDirective = "RESOLVE_NEED_YOU"
	ResumeDirectiveReviewDelivery ResumeDirective = "REVIEW_DELIVERY"
	ResumeDirectiveChooseRecovery ResumeDirective = "CHOOSE_RECOVERY"
	ResumeDirectiveViewOutcome    ResumeDirective = "VIEW_ACCEPTED_OUTCOME"
	ResumeDirectiveStopped        ResumeDirective = "STOPPED"
)

type HandoffNeedYouSummary struct {
	NeedYouID      string    `json:"needYouId"`
	Version        uint64    `json:"version"`
	Kind           string    `json:"kind"`
	ReasonCode     string    `json:"reasonCode"`
	Summary        string    `json:"summary"`
	Recommendation string    `json:"recommendation,omitempty"`
	AllowedActions []string  `json:"allowedActions"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// HandoffArtifactSummary contains immutable metadata only. A Host must call
// fetch_artifact for an authorized content reference; bytes never ride in a
// status response or chat transcript.
type HandoffArtifactSummary struct {
	ArtifactID  string `json:"artifactId"`
	LogicalName string `json:"logicalName"`
	MediaType   string `json:"mediaType"`
	ContentHash string `json:"contentHash"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type HandoffVerificationSummary struct {
	VerificationID string                     `json:"verificationId"`
	CriterionID    string                     `json:"criterionId"`
	ArtifactID     string                     `json:"artifactId"`
	Status         string                     `json:"status"`
	Code           string                     `json:"code"`
	Summary        string                     `json:"summary"`
	Independence   string                     `json:"independence"`
	EvidenceRefs   []HandoffEvidenceReference `json:"evidenceRefs,omitempty"`
}

type HandoffEvidenceReference struct {
	Kind         string `json:"kind"`
	Reference    string `json:"reference"`
	Reproducible bool   `json:"reproducible"`
}

// HandoffDeliverySummary is the browser/Host-safe subset of the Provider's
// immutable delivery receipt. Provider member identity and execution internals
// deliberately remain outside this projection.
type HandoffDeliverySummary struct {
	ArtifactID   string                     `json:"artifactId"`
	Comment      string                     `json:"comment"`
	EvidenceRefs []HandoffEvidenceReference `json:"evidenceRefs"`
	SubmittedAt  time.Time                  `json:"submittedAt"`
}

type HandoffRecoveryOption struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type HandoffRecoverySummary struct {
	WorkUnitID string                  `json:"workUnitId"`
	GapID      string                  `json:"gapId"`
	ReasonCode string                  `json:"reasonCode"`
	Summary    string                  `json:"summary"`
	Options    []HandoffRecoveryOption `json:"options,omitempty"`
}

// HandoffContextSummary is the minimum metadata a consumer needs to understand
// what crossed the responsibility boundary. It intentionally omits the source
// URI, content hash and Disclosure authority reference.
type HandoffContextSummary struct {
	Name        string      `json:"name"`
	MediaType   string      `json:"mediaType,omitempty"`
	Sensitivity Sensitivity `json:"sensitivity"`
}

// HandoffControlSummary explains the immutable delivery contract without
// exposing Provider endpoints, credentials or raw execution policy objects.
type HandoffControlSummary struct {
	ServiceVersionID   string                  `json:"serviceVersionId"`
	BindingID          string                  `json:"bindingId"`
	BindingVersion     string                  `json:"bindingVersion"`
	ExpectedArtifacts  []ArtifactExpectation   `json:"expectedArtifacts"`
	Inputs             []HandoffContextSummary `json:"inputs,omitempty"`
	RequiredActions    []string                `json:"requiredActions,omitempty"`
	RequiredDataScopes []string                `json:"requiredDataScopes,omitempty"`
	VerifierKinds      []string                `json:"verifierKinds,omitempty"`
	EstimatedCostMinor *int64                  `json:"estimatedCostMinor,omitempty"`
	Currency           string                  `json:"currency,omitempty"`
}

type HandoffOutcomeSummary struct {
	OutcomeID   string    `json:"outcomeId"`
	ContentHash string    `json:"contentHash"`
	AcceptedBy  string    `json:"acceptedBy"`
	AcceptedAt  time.Time `json:"acceptedAt"`
}

// HandoffResumePayload is bounded, caller-scoped and reconstructable from the
// Server fact model. Hosts may cache it but must refresh before a mutation.
type HandoffResumePayload struct {
	Next           ResumeDirective              `json:"next"`
	PendingNeedYou []HandoffNeedYouSummary      `json:"pendingNeedYou,omitempty"`
	Artifacts      []HandoffArtifactSummary     `json:"artifacts,omitempty"`
	Verifications  []HandoffVerificationSummary `json:"verifications,omitempty"`
	Deliveries     []HandoffDeliverySummary     `json:"deliveries,omitempty"`
	Recovery       *HandoffRecoverySummary      `json:"recovery,omitempty"`
	Outcome        *HandoffOutcomeSummary       `json:"outcome,omitempty"`
}

type HandoffStatusView struct {
	HandoffRef
	SourceHostKind HostKind             `json:"sourceHostKind"`
	Goal           string               `json:"goal"`
	MissionVersion uint64               `json:"missionVersion"`
	UpdateCursor   string               `json:"updateCursor"`
	Changed        bool                 `json:"changed"`
	ArtifactRefs   []string             `json:"artifactRefs,omitempty"`
	Resume         HandoffResumePayload `json:"resume"`
	SafeSummary    string               `json:"safeSummary"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

// HandoffConsumerView is the explicit allowlist shared by browser-facing
// adapters. It intentionally has no Mission, Provider execution, endpoint,
// credential, or raw-error fields.
type HandoffConsumerView struct {
	HandoffID           string                       `json:"handoffId"`
	SourceHostKind      HostKind                     `json:"sourceHostKind"`
	Goal                string                       `json:"goal"`
	Status              HandoffStatus                `json:"status"`
	Version             uint64                       `json:"version"`
	PortalURL           string                       `json:"portalUrl"`
	ProviderDisplayName string                       `json:"providerDisplayName"`
	Runtime             string                       `json:"runtime"`
	Delivery            string                       `json:"delivery"`
	Verification        string                       `json:"verification"`
	Acceptance          string                       `json:"acceptance"`
	AllowedActions      []string                     `json:"allowedActions"`
	PollAfterMillis     int64                        `json:"pollAfterMillis"`
	UpdateCursor        string                       `json:"updateCursor"`
	NeedYou             []HandoffNeedYouSummary      `json:"needYou,omitempty"`
	Artifacts           []HandoffArtifactSummary     `json:"artifacts,omitempty"`
	Verifications       []HandoffVerificationSummary `json:"verifications,omitempty"`
	Deliveries          []HandoffDeliverySummary     `json:"deliveries,omitempty"`
	Recovery            *HandoffRecoverySummary      `json:"recovery,omitempty"`
	Control             HandoffControlSummary        `json:"control"`
	SafeSummary         string                       `json:"safeSummary"`
	CreatedAt           time.Time                    `json:"createdAt"`
	UpdatedAt           time.Time                    `json:"updatedAt"`
}

// HandoffListItem is a bounded discovery projection. It deliberately omits
// context and Artifact bodies; Hosts must query Status before any decision.
type HandoffListItem struct {
	HandoffRef
	SourceHostKind HostKind        `json:"sourceHostKind"`
	Goal           string          `json:"goal"`
	MissionVersion uint64          `json:"missionVersion"`
	Next           ResumeDirective `json:"next"`
	SafeSummary    string          `json:"safeSummary"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

type ListHandoffsResponse struct {
	ContractVersion string            `json:"contractVersion"`
	Items           []HandoffListItem `json:"items"`
}

type FetchArtifactRequest struct {
	ContractVersion string        `json:"contractVersion"`
	Caller          CallerContext `json:"caller"`
	HandoffID       string        `json:"handoffId"`
	ArtifactID      string        `json:"artifactId"`
}

type ArtifactReference struct {
	ContractVersion string `json:"contractVersion"`
	ArtifactID      string `json:"artifactId"`
	LogicalName     string `json:"logicalName"`
	MediaType       string `json:"mediaType"`
	ContentHash     string `json:"contentHash"`
	ContentURI      string `json:"contentUri,omitempty"`
	PreviewURI      string `json:"previewUri,omitempty"`
}

type DeliveryDecisionRequest struct {
	ContractVersion string        `json:"contractVersion"`
	Caller          CallerContext `json:"caller"`
	HandoffID       string        `json:"handoffId"`
	MissionVersion  uint64        `json:"missionVersion"`
	IdempotencyKey  string        `json:"idempotencyKey"`
	Reason          string        `json:"reason,omitempty"`
}

type DeliveryDecisionReceipt struct {
	ContractVersion string        `json:"contractVersion"`
	HandoffID       string        `json:"handoffId"`
	Status          HandoffStatus `json:"status"`
	MissionVersion  uint64        `json:"missionVersion"`
	RecordedAt      time.Time     `json:"recordedAt"`
}

type ProvideHandoffInputRequest struct {
	ContractVersion string        `json:"contractVersion"`
	Caller          CallerContext `json:"caller"`
	HandoffID       string        `json:"handoffId"`
	NeedYouID       string        `json:"needYouId"`
	NeedYouVersion  uint64        `json:"needYouVersion"`
	Action          string        `json:"action"`
	Input           string        `json:"input"`
	IdempotencyKey  string        `json:"idempotencyKey"`
}
