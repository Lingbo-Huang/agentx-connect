package agentbridge

import (
	"encoding/json"
	"errors"
)

type ErrorCode string

const (
	ErrorCodeValidationFailed       ErrorCode = "VALIDATION_FAILED"
	ErrorCodeAuthenticationRequired ErrorCode = "AUTHENTICATION_REQUIRED"
	ErrorCodeAuthorizationDenied    ErrorCode = "AUTHORIZATION_DENIED"
	ErrorCodeCapabilityNotFound     ErrorCode = "CAPABILITY_NOT_FOUND"
	ErrorCodeConnectionRequired     ErrorCode = "CONNECTION_REQUIRED"
	ErrorCodePolicyDenied           ErrorCode = "POLICY_DENIED"
	ErrorCodeBudgetExceeded         ErrorCode = "BUDGET_EXCEEDED"
	ErrorCodeConflict               ErrorCode = "CONFLICT"
	ErrorCodeRateLimited            ErrorCode = "RATE_LIMITED"
	ErrorCodeProviderUnavailable    ErrorCode = "PROVIDER_UNAVAILABLE"
	ErrorCodeSideEffectUnknown      ErrorCode = "SIDE_EFFECT_UNKNOWN"
	ErrorCodeInternal               ErrorCode = "INTERNAL_ERROR"
)

// Error carries only Host-safe information. Internal stack traces, provider
// credentials and raw payloads belong in the server-side trace.
type Error struct {
	Code        ErrorCode `json:"code"`
	SafeSummary string    `json:"safeSummary"`
	Retryable   bool      `json:"retryable"`
	NeedsUser   bool      `json:"userActionRequired"`
	ResourceRef string    `json:"resourceRef,omitempty"`
	DeepLink    string    `json:"deepLink,omitempty"`
}

func (err *Error) Error() string {
	if err == nil {
		return ""
	}
	// MCP turns ordinary tool errors into text content. Keep that fallback
	// machine-readable so Hosts retain retry, user-action and navigation
	// metadata instead of seeing only a prose summary.
	encoded, marshalErr := json.Marshal(err)
	if marshalErr == nil {
		return string(encoded)
	}
	return string(err.Code) + ": " + err.SafeSummary
}

func NewError(code ErrorCode, safeSummary string, retryable, needsUser bool, resourceRef string) error {
	return &Error{Code: code, SafeSummary: safeSummary, Retryable: retryable, NeedsUser: needsUser, ResourceRef: resourceRef}
}

func NewUserActionError(code ErrorCode, safeSummary, resourceRef, deepLink string) error {
	return &Error{
		Code: code, SafeSummary: safeSummary, Retryable: false, NeedsUser: true,
		ResourceRef: resourceRef, DeepLink: deepLink,
	}
}

func IsErrorCode(err error, code ErrorCode) bool {
	var bridgeError *Error
	return errors.As(err, &bridgeError) && bridgeError.Code == code
}

func AsError(err error, target **Error) bool {
	return errors.As(err, target)
}
