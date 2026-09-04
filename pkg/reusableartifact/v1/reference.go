// Package reusableartifact defines the stable, opaque reference used when a
// canonical Artifact from one Mission becomes read-only input to another.
package reusableartifact

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

const (
	ContractVersion = "reusable-artifact.agentx.dev/v1"
	SourceScheme    = "agentx-artifact"
	SourceAuthority = "scope"
)

var ErrInvalidSourceURI = errors.New("invalid reusable Artifact source URI")

type Reference struct {
	MissionID  string
	WorkUnitID string
	ArtifactID string
}

func SourceURI(reference Reference) (string, error) {
	if !validID(reference.MissionID) || !validID(reference.WorkUnitID) || !validID(reference.ArtifactID) {
		return "", ErrInvalidSourceURI
	}
	return (&url.URL{
		Scheme: SourceScheme,
		Host:   SourceAuthority,
		Path:   "/missions/" + reference.MissionID + "/work-units/" + reference.WorkUnitID + "/artifacts/" + reference.ArtifactID,
	}).String(), nil
}

func ParseSourceURI(value string) (Reference, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != SourceScheme || parsed.Host != SourceAuthority || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return Reference{}, ErrInvalidSourceURI
	}
	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(segments) != 6 || segments[0] != "missions" || segments[2] != "work-units" || segments[4] != "artifacts" {
		return Reference{}, ErrInvalidSourceURI
	}
	reference := Reference{MissionID: segments[1], WorkUnitID: segments[3], ArtifactID: segments[5]}
	if !validID(reference.MissionID) || !validID(reference.WorkUnitID) || !validID(reference.ArtifactID) {
		return Reference{}, ErrInvalidSourceURI
	}
	return reference, nil
}

func IsSourceURI(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), SourceScheme+"://")
}

func validID(value string) bool {
	if value == "" || value == "." || value == ".." || strings.Contains(value, "..") || len(value) > 200 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("-_.:", character) {
			continue
		}
		return false
	}
	return true
}
