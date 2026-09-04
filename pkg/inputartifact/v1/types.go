// Package inputartifact defines the versioned browser-to-Dock contract for
// input files that are not already inside an authorized workspace.
package inputartifact

import (
	"net/url"
	"strings"
	"time"
)

const (
	ContractVersion            = "input-artifact.agentx.dev/v1"
	LocalURIPrefix             = "agentx-input://local/"
	MaxUploadBytes       int64 = 16 << 20
	localInputPathPrefix       = "/input_"
)

type Sensitivity string

const SensitivityConfidential Sensitivity = "CONFIDENTIAL"

type Receipt struct {
	ContractVersion string      `json:"contractVersion"`
	SourceRef       string      `json:"sourceRef"`
	DisplayName     string      `json:"displayName"`
	MediaType       string      `json:"mediaType"`
	SizeBytes       int64       `json:"sizeBytes"`
	ContentHash     string      `json:"contentHash"`
	Sensitivity     Sensitivity `json:"sensitivity"`
	CreatedAt       time.Time   `json:"createdAt"`
}

func SourceURI(id string) string {
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "input_") {
		id = "input_" + id
	}
	return LocalURIPrefix + id
}

func IDFromSourceURI(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "agentx-input" || parsed.Host != "local" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if !strings.HasPrefix(parsed.Path, localInputPathPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(parsed.Path, "/")
	if len(id) != len("input_")+32 || parsed.Path != "/"+id {
		return "", false
	}
	for _, character := range strings.TrimPrefix(id, "input_") {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", false
		}
	}
	return id, true
}
