// Client wire types are separate from the authorization service for public distribution.
package hostauth

import (
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
	"errors"
	"time"
)

var (
	ErrAuthorizationPending  = errors.New("device authorization is pending")
	ErrAuthorizationSlowDown = errors.New("device authorization polling is too frequent")
	ErrAuthorizationDenied   = errors.New("device authorization was denied")
	ErrAuthorizationExpired  = errors.New("device authorization expired")
	ErrAuthorizationConsumed = errors.New("device authorization was already consumed")
	ErrAuthorizationNotFound = errors.New("device authorization does not exist")
)

type DeviceAuthorizationStart struct {
	DeviceCode      string    `json:"deviceCode"`
	UserCode        string    `json:"userCode"`
	VerificationURI string    `json:"verificationUri"`
	ExpiresAt       time.Time `json:"expiresAt"`
	IntervalSeconds int64     `json:"intervalSeconds"`
}

type DeviceAuthorizationToken struct {
	Credential string                 `json:"credential"`
	Caller     protocol.CallerContext `json:"caller"`
	ExpiresAt  time.Time              `json:"expiresAt"`
}
