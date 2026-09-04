package agentbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	inputartifact "github.com/Lingbo-Huang/agentx-connect/pkg/inputartifact/v1"
	reusableartifact "github.com/Lingbo-Huang/agentx-connect/pkg/reusableartifact/v1"
)

const (
	maxIDBytes          = 160
	maxTokenBytes       = 160
	maxReasonBytes      = 1_024
	maxGoalBytes        = 16 << 10
	maxContextRefs      = 32
	maxArtifactTypes    = 32
	maxControlValues    = 64
	maxIdempotencyBytes = 200
	defaultSearchLimit  = 10
	maximumSearchLimit  = 20
)

func ValidateSearchCapabilitiesRequest(request SearchCapabilitiesRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateText("query", request.Query, MaxQueryBytes); err != nil {
		return err
	}
	if len(request.ExpectedArtifactTypes) > maxArtifactTypes || !uniqueTokens(request.ExpectedArtifactTypes) {
		return validationError("expectedArtifactTypes are invalid")
	}
	if !validSensitivity(request.MaximumSensitivity) {
		return validationError("maximumSensitivity is invalid")
	}
	if request.Mode != RoutingModeEconomy && request.Mode != RoutingModeBalanced && request.Mode != RoutingModeEffect && request.Mode != RoutingModeFast {
		return validationError("mode is invalid")
	}
	if request.BudgetMaximumMinor != nil {
		if *request.BudgetMaximumMinor < 0 || strings.TrimSpace(request.BudgetCurrency) == "" {
			return validationError("budget is invalid")
		}
	} else if request.BudgetCurrency != "" {
		return validationError("budgetCurrency requires budgetMaximumMinor")
	}
	if request.Deadline != nil && request.Deadline.IsZero() {
		return validationError("deadline is invalid")
	}
	if request.Limit > maximumSearchLimit {
		return validationError("limit exceeds the protocol maximum")
	}
	return nil
}

func ValidateInvokeCapabilityRequest(request InvokeCapabilityRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateToken("bindingId", request.BindingID, maxIDBytes); err != nil {
		return err
	}
	if err := validateToken("bindingVersion", request.BindingVersion, maxTokenBytes); err != nil {
		return err
	}
	if err := validateToken("idempotencyKey", request.IdempotencyKey, maxIdempotencyBytes); err != nil {
		return err
	}
	if err := validateBoundedJSONObject(request.Input, MaxInvokeInputBytes); err != nil {
		return validationError("input must be one bounded JSON object")
	}
	return nil
}

func ValidateInvocationReceipt(receipt InvocationReceipt) error {
	if receipt.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{name: "invocationId", value: receipt.InvocationID, limit: maxIDBytes},
		{name: "bindingId", value: receipt.BindingID, limit: maxIDBytes},
		{name: "bindingVersion", value: receipt.BindingVersion, limit: maxTokenBytes},
		{name: "traceId", value: receipt.TraceID, limit: maxIDBytes},
	} {
		if err := validateToken(field.name, field.value, field.limit); err != nil {
			return err
		}
	}
	if err := validateText("safeSummary", receipt.SafeSummary, maxReasonBytes); err != nil {
		return err
	}
	if receipt.StartedAt.IsZero() || receipt.InputBytes <= 0 || receipt.InputBytes > MaxInvokeInputBytes ||
		receipt.OutputBytes < 0 || receipt.OutputBytes > MaxInvokeOutputBytes || receipt.DurationMillis < 0 {
		return validationError("invocation timing or byte usage is invalid")
	}
	if len(receipt.ArtifactRefs) > maxArtifactTypes || !uniqueOptionalTokens(receipt.ArtifactRefs) {
		return validationError("artifactRefs are invalid")
	}
	if receipt.CostMinor != nil {
		if *receipt.CostMinor < 0 || strings.TrimSpace(receipt.Currency) == "" {
			return validationError("invocation cost is invalid")
		}
	} else if receipt.Currency != "" {
		return validationError("currency requires costMinor")
	}
	switch receipt.Status {
	case InvocationStatusUnknown:
		if receipt.FinishedAt != nil || receipt.DurationMillis != 0 || receipt.OutputBytes != 0 ||
			len(receipt.Output) != 0 || receipt.OutputMediaType != "" || receipt.CostMinor != nil || receipt.Currency != "" {
			return validationError("unknown invocation contains terminal facts")
		}
	case InvocationStatusSucceeded, InvocationStatusFailed:
		if receipt.FinishedAt == nil || receipt.FinishedAt.Before(receipt.StartedAt) ||
			receipt.DurationMillis != receipt.FinishedAt.Sub(receipt.StartedAt).Milliseconds() {
			return validationError("terminal invocation timing is invalid")
		}
		if len(receipt.Output) == 0 {
			if receipt.OutputBytes != 0 || receipt.OutputMediaType != "" {
				return validationError("invocation output metadata is invalid")
			}
		} else {
			if err := validateToken("outputMediaType", receipt.OutputMediaType, maxTokenBytes); err != nil {
				return err
			}
			if int64(len(receipt.Output)) != receipt.OutputBytes || validateBoundedJSONObject(receipt.Output, MaxInvokeOutputBytes) != nil {
				return validationError("invocation output is invalid")
			}
		}
		if receipt.Status == InvocationStatusSucceeded && len(receipt.Output) == 0 && len(receipt.ArtifactRefs) == 0 {
			return validationError("successful invocation requires output or an Artifact")
		}
	default:
		return validationError("invocation status is invalid")
	}
	return nil
}

func ValidateHandoffWorkRequest(request HandoffWorkRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateToken("bindingId", request.BindingID, maxIDBytes); err != nil {
		return err
	}
	if err := validateToken("bindingVersion", request.BindingVersion, maxTokenBytes); err != nil {
		return err
	}
	if err := validateToken("idempotencyKey", request.IdempotencyKey, maxIdempotencyBytes); err != nil {
		return err
	}
	if err := validateText("goal", request.Goal, maxGoalBytes); err != nil {
		return err
	}
	if len(request.Context) > maxContextRefs {
		return validationError("context exceeds the protocol maximum")
	}
	contextNames := make(map[string]struct{}, len(request.Context))
	for _, reference := range request.Context {
		if _, duplicate := contextNames[reference.Name]; duplicate {
			return validationError("context contains duplicate names")
		}
		contextNames[reference.Name] = struct{}{}
		if err := ValidateContextReference(reference); err != nil {
			return err
		}
	}
	if err := validateArtifactExpectations(request.ExpectedArtifacts); err != nil {
		return err
	}
	if request.Deadline != nil && request.Deadline.IsZero() {
		return validationError("deadline is invalid")
	}
	return nil
}

// ValidateContextReference validates the immutable metadata identity used by a
// version-bound disclosure. It does not fetch the referenced content.
func ValidateContextReference(reference ContextReference) error {
	if err := validateText("context name", reference.Name, 255); err != nil {
		return err
	}
	if err := validateContextURI(reference.URI); err != nil {
		return err
	}
	if err := validateToken("context mediaType", reference.MediaType, maxTokenBytes); err != nil {
		return err
	}
	if reference.SizeBytes <= 0 || reference.SizeBytes > MaxContextSizeBytes {
		return validationError("context sizeBytes is invalid")
	}
	if !validSHA256(reference.ContentHash) {
		return validationError("context contentHash is invalid")
	}
	if !validSensitivity(reference.Sensitivity) {
		return validationError("context sensitivity is invalid")
	}
	return nil
}

func ValidateGetHandoffStatusRequest(request GetHandoffStatusRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateToken("handoffId", request.HandoffID, maxIDBytes); err != nil {
		return err
	}
	if request.KnownUpdateCursor != "" && !validSHA256(request.KnownUpdateCursor) {
		return validationError("knownUpdateCursor is invalid")
	}
	return nil
}

func ValidateListHandoffsRequest(request ListHandoffsRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if request.Limit > MaxHandoffListLimit {
		return validationError("handoff list limit exceeds the protocol maximum")
	}
	return nil
}

func ValidateListHandoffsResponse(response ListHandoffsResponse) error {
	if response.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if len(response.Items) > MaxHandoffListLimit {
		return validationError("handoff list exceeds the protocol maximum")
	}
	for index, item := range response.Items {
		if err := ValidateHandoffRef(item.HandoffRef); err != nil {
			return err
		}
		if !validHostKind(item.SourceHostKind) {
			return validationError("handoff source Host kind is invalid")
		}
		if err := validateText("goal", item.Goal, maxGoalBytes); err != nil {
			return err
		}
		if item.MissionVersion == 0 || !validResumeDirective(item.Next) || !resumeDirectiveMatchesStatus(item.Next, item.Status) {
			return validationError("handoff list item state is invalid")
		}
		if err := validateText("safeSummary", item.SafeSummary, maxReasonBytes); err != nil {
			return err
		}
		if item.UpdatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return validationError("handoff list item updatedAt is invalid")
		}
		if index > 0 {
			previous := response.Items[index-1]
			if previous.UpdatedAt.Before(item.UpdatedAt) ||
				(previous.UpdatedAt.Equal(item.UpdatedAt) && previous.HandoffID < item.HandoffID) {
				return validationError("handoff list ordering is invalid")
			}
		}
	}
	return nil
}

func ValidateHandoffStatusView(view HandoffStatusView) error {
	if err := ValidateHandoffRef(view.HandoffRef); err != nil {
		return err
	}
	if !validHostKind(view.SourceHostKind) {
		return validationError("handoff source Host kind is invalid")
	}
	if err := validateText("goal", view.Goal, maxGoalBytes); err != nil {
		return err
	}
	if view.MissionVersion == 0 {
		return validationError("missionVersion is required")
	}
	if !validSHA256(view.UpdateCursor) {
		return validationError("updateCursor is invalid")
	}
	if len(view.ArtifactRefs) > maxArtifactTypes || !uniqueOptionalTokens(view.ArtifactRefs) {
		return validationError("artifactRefs are invalid")
	}
	if err := validateHandoffResume(view.Status, view.ArtifactRefs, view.Resume); err != nil {
		return err
	}
	if err := validateText("safeSummary", view.SafeSummary, maxReasonBytes); err != nil {
		return err
	}
	if view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.CreatedAt) {
		return validationError("updatedAt is invalid")
	}
	return nil
}

func ValidateHandoffConsumerView(view HandoffConsumerView) error {
	if err := validateToken("handoffId", view.HandoffID, maxIDBytes); err != nil {
		return err
	}
	if !validHostKind(view.SourceHostKind) || !validHandoffStatus(view.Status) || view.Version == 0 {
		return validationError("handoff consumer state is invalid")
	}
	if err := validateText("goal", view.Goal, maxGoalBytes); err != nil {
		return err
	}
	if err := validateHandoffControlSummary(view.Control); err != nil {
		return err
	}
	if !validSHA256(view.UpdateCursor) || view.PollAfterMillis < 0 {
		return validationError("handoff consumer cursor is invalid")
	}
	if err := validateText("safeSummary", view.SafeSummary, maxReasonBytes); err != nil {
		return err
	}
	if view.CreatedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.CreatedAt) {
		return validationError("handoff consumer timestamps are invalid")
	}
	return nil
}

func validateHandoffControlSummary(value HandoffControlSummary) error {
	if err := validateToken("serviceVersionId", value.ServiceVersionID, maxIDBytes); err != nil {
		return err
	}
	if err := validateToken("bindingId", value.BindingID, maxIDBytes); err != nil {
		return err
	}
	if err := validateToken("bindingVersion", value.BindingVersion, maxTokenBytes); err != nil {
		return err
	}
	if err := validateArtifactExpectations(value.ExpectedArtifacts); err != nil {
		return err
	}
	if len(value.Inputs) > maxContextRefs || len(value.RequiredActions) > maxControlValues ||
		len(value.RequiredDataScopes) > maxControlValues || len(value.VerifierKinds) > maxControlValues {
		return validationError("handoff control projection exceeds the protocol maximum")
	}
	for _, input := range value.Inputs {
		if err := validateText("context name", input.Name, 255); err != nil {
			return err
		}
		if input.MediaType != "" {
			if err := validateToken("context mediaType", input.MediaType, maxTokenBytes); err != nil {
				return err
			}
		}
		if !validSensitivity(input.Sensitivity) {
			return validationError("context sensitivity is invalid")
		}
	}
	if value.EstimatedCostMinor != nil && (*value.EstimatedCostMinor < 0 || strings.TrimSpace(value.Currency) == "") {
		return validationError("handoff estimated cost is invalid")
	}
	if value.EstimatedCostMinor == nil && value.Currency != "" {
		return validationError("handoff currency requires an estimated cost")
	}
	return nil
}

func validateHandoffResume(status HandoffStatus, artifactRefs []string, resume HandoffResumePayload) error {
	if !validResumeDirective(resume.Next) || !resumeDirectiveMatchesStatus(resume.Next, status) {
		return validationError("resume directive is invalid for handoff status")
	}
	if len(resume.PendingNeedYou) > maxArtifactTypes || len(resume.Artifacts) > maxArtifactTypes ||
		len(resume.Verifications) > maxArtifactTypes || len(resume.Deliveries) > maxArtifactTypes {
		return validationError("resume projection exceeds the protocol maximum")
	}
	if resume.Next == ResumeDirectiveResolveNeedYou && len(resume.PendingNeedYou) == 0 {
		return validationError("Need You resume requires a pending item")
	}
	if resume.Next != ResumeDirectiveResolveNeedYou && len(resume.PendingNeedYou) != 0 {
		return validationError("pending Need You does not match resume directive")
	}
	seenNeedYou := make(map[string]struct{}, len(resume.PendingNeedYou))
	for _, item := range resume.PendingNeedYou {
		if err := validateToken("needYouId", item.NeedYouID, maxIDBytes); err != nil || item.Version == 0 || item.ExpiresAt.IsZero() {
			return validationError("pending Need You is invalid")
		}
		if _, duplicate := seenNeedYou[item.NeedYouID]; duplicate {
			return validationError("pending Need You contains duplicate IDs")
		}
		seenNeedYou[item.NeedYouID] = struct{}{}
		if err := validateToken("Need You kind", item.Kind, maxTokenBytes); err != nil {
			return err
		}
		if err := validateToken("Need You reasonCode", item.ReasonCode, maxTokenBytes); err != nil {
			return err
		}
		if err := validateText("Need You summary", item.Summary, maxReasonBytes); err != nil {
			return err
		}
		if err := validateOptionalText("Need You recommendation", item.Recommendation, maxReasonBytes); err != nil {
			return err
		}
		if len(item.AllowedActions) == 0 || len(item.AllowedActions) > 8 || !uniqueTokens(item.AllowedActions) {
			return validationError("Need You actions are invalid")
		}
	}

	artifactSet := make(map[string]struct{}, len(resume.Artifacts))
	for _, artifact := range resume.Artifacts {
		if err := validateToken("resume artifactId", artifact.ArtifactID, maxIDBytes); err != nil {
			return err
		}
		if _, duplicate := artifactSet[artifact.ArtifactID]; duplicate {
			return validationError("resume Artifacts contain duplicate IDs")
		}
		artifactSet[artifact.ArtifactID] = struct{}{}
		if err := validateText("resume logicalName", artifact.LogicalName, 255); err != nil {
			return err
		}
		if err := validateToken("resume mediaType", artifact.MediaType, maxTokenBytes); err != nil ||
			!validSHA256(artifact.ContentHash) || artifact.SizeBytes < 0 {
			return validationError("resume Artifact is invalid")
		}
	}
	if len(artifactSet) != len(artifactRefs) {
		return validationError("resume Artifacts do not match artifactRefs")
	}
	for _, artifactID := range artifactRefs {
		if _, exists := artifactSet[artifactID]; !exists {
			return validationError("resume Artifacts do not match artifactRefs")
		}
	}

	seenVerification := make(map[string]struct{}, len(resume.Verifications))
	for _, verification := range resume.Verifications {
		for name, value := range map[string]string{
			"verificationId": verification.VerificationID, "criterionId": verification.CriterionID,
			"verification artifactId": verification.ArtifactID, "verification status": verification.Status,
			"verification code": verification.Code, "verification independence": verification.Independence,
		} {
			if err := validateToken(name, value, maxIDBytes); err != nil {
				return err
			}
		}
		if _, duplicate := seenVerification[verification.VerificationID]; duplicate {
			return validationError("resume Verifications contain duplicate IDs")
		}
		seenVerification[verification.VerificationID] = struct{}{}
		if err := validateText("verification summary", verification.Summary, maxReasonBytes); err != nil {
			return err
		}
	}

	if resume.Next == ResumeDirectiveChooseRecovery {
		if resume.Recovery == nil {
			return validationError("recovery resume requires a Gap")
		}
	} else if resume.Recovery != nil {
		return validationError("recovery projection does not match resume directive")
	}
	if resume.Recovery != nil {
		for name, value := range map[string]string{
			"recovery workUnitId": resume.Recovery.WorkUnitID, "recovery gapId": resume.Recovery.GapID,
			"recovery reasonCode": resume.Recovery.ReasonCode,
		} {
			if err := validateToken(name, value, maxIDBytes); err != nil {
				return err
			}
		}
		if err := validateText("recovery summary", resume.Recovery.Summary, maxReasonBytes); err != nil {
			return err
		}
		if len(resume.Recovery.Options) > 8 {
			return validationError("recovery options exceed the protocol maximum")
		}
		for _, option := range resume.Recovery.Options {
			if err := validateToken("recovery kind", option.Kind, maxTokenBytes); err != nil {
				return err
			}
			if err := validateText("recovery option summary", option.Summary, maxReasonBytes); err != nil {
				return err
			}
		}
	}

	if resume.Next == ResumeDirectiveViewOutcome {
		if resume.Outcome == nil {
			return validationError("accepted resume requires an Outcome")
		}
	} else if resume.Outcome != nil {
		return validationError("Outcome projection does not match resume directive")
	}
	if resume.Outcome != nil {
		if err := validateToken("outcomeId", resume.Outcome.OutcomeID, maxIDBytes); err != nil ||
			!validSHA256(resume.Outcome.ContentHash) || resume.Outcome.AcceptedAt.IsZero() {
			return validationError("resume Outcome is invalid")
		}
		if err := validateToken("acceptedBy", resume.Outcome.AcceptedBy, maxIDBytes); err != nil {
			return err
		}
	}
	return nil
}

func ValidateFetchArtifactRequest(request FetchArtifactRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateToken("handoffId", request.HandoffID, maxIDBytes); err != nil {
		return err
	}
	return validateToken("artifactId", request.ArtifactID, maxIDBytes)
}

func ValidateArtifactReference(reference ArtifactReference) error {
	if reference.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateToken("artifactId", reference.ArtifactID, maxIDBytes); err != nil {
		return err
	}
	if err := validateText("logicalName", reference.LogicalName, 255); err != nil {
		return err
	}
	if err := validateToken("mediaType", reference.MediaType, maxTokenBytes); err != nil {
		return err
	}
	if !validSHA256(reference.ContentHash) {
		return validationError("contentHash is invalid")
	}
	for name, value := range map[string]string{"contentUri": reference.ContentURI, "previewUri": reference.PreviewURI} {
		if value != "" {
			if err := validateSafeProjectionURI(name, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateDeliveryDecisionRequest(request DeliveryDecisionRequest) error {
	if request.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateCaller(request.Caller); err != nil {
		return err
	}
	if err := validateToken("handoffId", request.HandoffID, maxIDBytes); err != nil {
		return err
	}
	if request.MissionVersion == 0 {
		return validationError("missionVersion is required")
	}
	if err := validateToken("idempotencyKey", request.IdempotencyKey, maxIdempotencyBytes); err != nil {
		return err
	}
	return validateOptionalText("reason", request.Reason, maxReasonBytes)
}

func ValidateDeliveryDecisionReceipt(receipt DeliveryDecisionReceipt) error {
	if receipt.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateToken("handoffId", receipt.HandoffID, maxIDBytes); err != nil {
		return err
	}
	if !validHandoffStatus(receipt.Status) || receipt.MissionVersion == 0 || receipt.RecordedAt.IsZero() {
		return validationError("delivery decision receipt is invalid")
	}
	return nil
}

func validateBoundedJSON(value json.RawMessage, maximum int) error {
	if len(value) == 0 || len(value) > maximum || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("JSON value is empty, null, or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains multiple values")
	}
	return nil
}

func validateBoundedJSONObject(value json.RawMessage, maximum int) error {
	if err := validateBoundedJSON(value, maximum); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if _, object := decoded.(map[string]any); !object {
		return errors.New("JSON value is not an object")
	}
	return nil
}

func NormalizeSearchLimit(limit uint32) uint32 {
	if limit == 0 {
		return defaultSearchLimit
	}
	if limit > maximumSearchLimit {
		return maximumSearchLimit
	}
	return limit
}

func ValidateSearchCapabilitiesResponse(response SearchCapabilitiesResponse) error {
	if response.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateText("safeSummary", response.SafeSummary, maxReasonBytes); err != nil {
		return err
	}
	if response.AutomaticFallbackAllowed {
		return validationError("automatic capability fallback is not allowed")
	}
	if len(response.Matches) > maximumSearchLimit {
		return validationError("matches exceed the protocol maximum")
	}
	switch response.Disposition {
	case SearchDispositionMatchesFound:
		if len(response.Matches) == 0 || response.NextAction != SearchNextActionSelectExactMatch {
			return validationError("matched search disposition is inconsistent")
		}
	case SearchDispositionCapabilityGap:
		if len(response.Matches) != 0 || response.NextAction != SearchNextActionAskUserOrReportGap {
			return validationError("capability gap disposition is inconsistent")
		}
	default:
		return validationError("search disposition is invalid")
	}
	seen := make(map[string]struct{}, len(response.Matches))
	for _, match := range response.Matches {
		if err := ValidateCapabilityMatch(match); err != nil {
			return err
		}
		key := match.BindingID + "\x00" + match.BindingVersion
		if _, duplicate := seen[key]; duplicate {
			return validationError("matches contain duplicate Binding versions")
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateCallerContext validates an already authenticated Host projection.
// Transports must inject this value from their credential/session and must not
// expose it as model-controlled tool input.
func ValidateCallerContext(caller CallerContext) error {
	return validateCaller(caller)
}

func ValidateCapabilityMatch(match CapabilityMatch) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "capabilityId", value: match.CapabilityID},
		{name: "serviceId", value: match.ServiceID},
		{name: "serviceVersionId", value: match.ServiceVersionID},
		{name: "bindingId", value: match.BindingID},
		{name: "bindingVersion", value: match.BindingVersion},
		{name: "providerId", value: match.Provider.ProviderID},
		{name: "provider displayName", value: match.Provider.DisplayName},
		{name: "provider kind", value: match.Provider.Kind},
	} {
		if err := validateToken(field.name, field.value, maxIDBytes); err != nil {
			return err
		}
	}
	if err := validateText("displayName", match.DisplayName, 255); err != nil {
		return err
	}
	if err := validateText("description", match.Description, maxReasonBytes); err != nil {
		return err
	}
	if len(match.CapabilityCodes) > maxArtifactTypes || !uniqueTokens(match.CapabilityCodes) {
		return validationError("capabilityCodes are invalid")
	}
	if match.ExecutionMode != ExecutionModeInvoke && match.ExecutionMode != ExecutionModeHandoff {
		return validationError("executionMode is invalid")
	}
	if len(match.ArtifactTypes) == 0 || len(match.ArtifactTypes) > maxArtifactTypes || !uniqueTokens(match.ArtifactTypes) {
		return validationError("artifactTypes are invalid")
	}
	if len(match.VerifierKinds) == 0 || len(match.VerifierKinds) > maxArtifactTypes || !uniqueTokens(match.VerifierKinds) {
		return validationError("verifierKinds are invalid")
	}
	if match.ConnectionState != ConnectionStateReady && match.ConnectionState != ConnectionStateRequired && match.ConnectionState != ConnectionStateUnavailable {
		return validationError("connectionState is invalid")
	}
	if match.GrantState != GrantStateGranted && match.GrantState != GrantStateRequired && match.GrantState != GrantStateDenied {
		return validationError("grantState is invalid")
	}
	if match.EstimatedDurationMillis < 0 || (match.EstimatedCostMinor != nil && *match.EstimatedCostMinor < 0) {
		return validationError("estimated duration or cost is invalid")
	}
	if match.EstimatedCostMinor != nil && strings.TrimSpace(match.Currency) == "" {
		return validationError("currency is required for a known cost")
	}
	if evidence := match.FulfillmentEvidence; evidence != nil {
		if evidence.Level != FulfillmentEvidenceColdStart && evidence.Level != FulfillmentEvidenceObserved && evidence.Level != FulfillmentEvidenceReliable {
			return validationError("fulfillment evidence level is invalid")
		}
		// RevisionCount counts revision requests, not independent outcomes. One
		// work unit may be revised repeatedly and ultimately accepted. Completed
		// execution awaiting acceptance is also a sample with no final decision.
		if uint64(evidence.AcceptedCount)+uint64(evidence.FailureCount) > uint64(evidence.SampleCount) ||
			(evidence.SampleCount == 0 && evidence.RevisionCount != 0) ||
			evidence.FirstPassAcceptanceBasis > 10_000 || evidence.AverageDurationMillis < 0 {
			return validationError("fulfillment evidence counters are invalid")
		}
		if (evidence.Level == FulfillmentEvidenceColdStart && evidence.SampleCount != 0) ||
			(evidence.Level == FulfillmentEvidenceObserved && (evidence.SampleCount == 0 || evidence.SampleCount >= 3)) ||
			(evidence.Level == FulfillmentEvidenceReliable && evidence.SampleCount < 3) {
			return validationError("fulfillment evidence level disagrees with samples")
		}
	}
	return validateOptionalText("reason", match.Reason, maxReasonBytes)
}

func ValidateHandoffRef(ref HandoffRef) error {
	if ref.ContractVersion != ContractVersion {
		return validationError("unsupported Agent Bridge contract version")
	}
	if err := validateToken("handoffId", ref.HandoffID, maxIDBytes); err != nil {
		return err
	}
	if err := validateToken("missionId", ref.MissionID, maxIDBytes); err != nil {
		return err
	}
	if !validHandoffStatus(ref.Status) {
		return validationError("handoff status is invalid")
	}
	if len(ref.ExpectedArtifacts) == 0 || len(ref.ExpectedArtifacts) > maxArtifactTypes {
		return validationError("expectedArtifacts are required")
	}
	for _, artifact := range ref.ExpectedArtifacts {
		if err := validateText("artifact logicalName", artifact.LogicalName, 255); err != nil {
			return err
		}
		if err := validateToken("artifact mediaType", artifact.MediaType, maxTokenBytes); err != nil {
			return err
		}
	}
	if err := validateProviderSummary(ref.Provider); err != nil {
		return err
	}
	if ref.CreatedAt.IsZero() {
		return validationError("createdAt is required")
	}
	if err := validateDeepLink(ref.DeepLink); err != nil {
		return err
	}
	if ref.PollAfterMillis < 0 {
		return validationError("pollAfter cannot be negative")
	}
	return nil
}

func validateArtifactExpectations(values []ArtifactExpectation) error {
	if len(values) == 0 || len(values) > maxArtifactTypes {
		return validationError("expectedArtifacts are required")
	}
	logicalNames := make(map[string]struct{}, len(values))
	for _, artifact := range values {
		if err := validateText("artifact logicalName", artifact.LogicalName, 255); err != nil {
			return err
		}
		if _, duplicate := logicalNames[artifact.LogicalName]; duplicate {
			return validationError("expectedArtifacts contain duplicate logical names")
		}
		logicalNames[artifact.LogicalName] = struct{}{}
		if err := validateToken("artifact mediaType", artifact.MediaType, maxTokenBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderSummary(provider ProviderSummary) error {
	if err := validateToken("providerId", provider.ProviderID, maxIDBytes); err != nil {
		return err
	}
	if err := validateText("provider displayName", provider.DisplayName, 255); err != nil {
		return err
	}
	return validateToken("provider kind", provider.Kind, maxTokenBytes)
}

func validateContextURI(value string) error {
	if reusableartifact.IsSourceURI(value) {
		if _, err := reusableartifact.ParseSourceURI(value); err == nil {
			return nil
		}
		return validationError("context URI is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return validationError("context URI is invalid")
	}
	if parsed.Scheme == "https" && parsed.Host != "" {
		return nil
	}
	if parsed.Scheme == "agentx" && parsed.Host != "" && parsed.Path != "" {
		return nil
	}
	if _, ok := inputartifact.IDFromSourceURI(value); ok {
		return nil
	}
	return validationError("context URI must be an HTTPS or AgentX-controlled reference")
}

func validateSafeProjectionURI(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return validationError(name + " is invalid")
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "https" || parsed.Host == "" {
			return validationError(name + " must use HTTPS")
		}
		return nil
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return validationError(name + " must be a same-origin path or HTTPS URL")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "sha256:") {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validateCaller(caller CallerContext) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "principalId", value: caller.PrincipalID},
		{name: "spaceId", value: caller.SpaceID},
		{name: "hostInstallationId", value: caller.HostInstallationID},
	} {
		if err := validateToken(field.name, field.value, maxIDBytes); err != nil {
			return err
		}
	}
	if !validHostKind(caller.HostKind) {
		return validationError("hostKind is invalid")
	}
	return nil
}

func validHostKind(value HostKind) bool {
	return value == HostKindCodex || value == HostKindClaude || value == HostKindSeal || value == HostKindCursor ||
		value == HostKindWorkBuddy || value == HostKindTrae || value == HostKindWeb || value == HostKindOther
}

func validateDeepLink(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return validationError("deepLink is invalid")
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "https" || parsed.Host == "" {
			return validationError("absolute deepLink must use HTTPS")
		}
		return nil
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") {
		return validationError("relative deepLink must be an absolute path")
	}
	return nil
}

func validSensitivity(value Sensitivity) bool {
	return value == SensitivityPublic || value == SensitivityInternal || value == SensitivityConfidential
}

func validHandoffStatus(value HandoffStatus) bool {
	switch value {
	case HandoffStatusQueued, HandoffStatusInProgress, HandoffStatusNeedsUser, HandoffStatusDelivered,
		HandoffStatusAccepted, HandoffStatusRecovery, HandoffStatusRejected, HandoffStatusCanceled:
		return true
	default:
		return false
	}
}

func validResumeDirective(value ResumeDirective) bool {
	switch value {
	case ResumeDirectiveWait, ResumeDirectiveResolveNeedYou, ResumeDirectiveReviewDelivery,
		ResumeDirectiveChooseRecovery, ResumeDirectiveViewOutcome, ResumeDirectiveStopped:
		return true
	default:
		return false
	}
}

func resumeDirectiveMatchesStatus(directive ResumeDirective, status HandoffStatus) bool {
	switch status {
	case HandoffStatusQueued, HandoffStatusInProgress:
		return directive == ResumeDirectiveWait
	case HandoffStatusNeedsUser:
		return directive == ResumeDirectiveResolveNeedYou
	case HandoffStatusDelivered:
		return directive == ResumeDirectiveReviewDelivery
	case HandoffStatusRecovery:
		return directive == ResumeDirectiveChooseRecovery
	case HandoffStatusAccepted:
		return directive == ResumeDirectiveViewOutcome
	case HandoffStatusRejected, HandoffStatusCanceled:
		return directive == ResumeDirectiveStopped
	default:
		return false
	}
}

func uniqueTokens(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateToken("value", value, maxTokenBytes); err != nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func uniqueOptionalTokens(values []string) bool {
	if len(values) == 0 {
		return true
	}
	return uniqueTokens(values)
}

func validateToken(name, value string, maximum int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return validationError(name + " is invalid")
	}
	return nil
}

func validateText(name, value string, maximum int) error {
	if strings.TrimSpace(value) == "" || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return validationError(name + " is invalid")
	}
	return nil
}

func validateOptionalText(name, value string, maximum int) error {
	if value == "" {
		return nil
	}
	return validateText(name, value, maximum)
}

func validationError(summary string) error {
	return NewError(ErrorCodeValidationFailed, summary, false, false, "")
}
