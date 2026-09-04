// Package hostauth owns HostInstallation credentials. Browser sessions and
// Dock connector credentials are deliberately different identity domains.
package hostauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

const maxCredentialFileBytes = 16 << 10

// CredentialBundle is the local-development projection consumed by the MCP
// process. Team deployments must replace this with revocable, persisted
// device authorization; this file is never a Team identity database.
type CredentialBundle struct {
	ServerURL  string                 `json:"serverUrl"`
	Credential string                 `json:"credential"`
	Caller     protocol.CallerContext `json:"caller"`
	ExpiresAt  *time.Time             `json:"expiresAt,omitempty"`
}

func LoadCredentialBundle(path string) (CredentialBundle, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return CredentialBundle{}, errors.New("HostInstallation credential file path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return CredentialBundle{}, fmt.Errorf("inspect HostInstallation credential file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return CredentialBundle{}, errors.New("HostInstallation credential file must be a regular non-symlink file")
	}
	if info.Size() > maxCredentialFileBytes {
		return CredentialBundle{}, errors.New("HostInstallation credential file exceeds the safe size limit")
	}
	if err := validateCredentialPermissions(path, info); err != nil {
		return CredentialBundle{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return CredentialBundle{}, fmt.Errorf("open HostInstallation credential file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxCredentialFileBytes+1))
	decoder.DisallowUnknownFields()
	var value CredentialBundle
	if err := decoder.Decode(&value); err != nil {
		return CredentialBundle{}, errors.New("HostInstallation credential file is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CredentialBundle{}, errors.New("HostInstallation credential file must contain one JSON object")
	}
	if strings.TrimSpace(value.ServerURL) == "" || strings.TrimSpace(value.Credential) == "" {
		return CredentialBundle{}, errors.New("HostInstallation credential file is incomplete")
	}
	if err := protocol.ValidateCallerContext(value.Caller); err != nil {
		return CredentialBundle{}, errors.New("HostInstallation identity is invalid")
	}
	return value, nil
}

// SaveCredentialBundle persists a Team device credential without ever
// exposing it through command arguments or logs. First-time writes are
// exclusive; replacement is an explicit caller decision and is committed by
// atomic rename. Team authority remains server-side: this file is only a
// private local projection that can be revoked independently.
func SaveCredentialBundle(path string, value CredentialBundle, replace bool) error {
	path = strings.TrimSpace(path)
	value.ServerURL = strings.TrimSpace(value.ServerURL)
	value.Credential = strings.TrimSpace(value.Credential)
	if path == "" {
		return errors.New("HostInstallation credential file path is required")
	}
	if value.ServerURL == "" || value.Credential == "" {
		return errors.New("HostInstallation credential is incomplete")
	}
	if err := protocol.ValidateCallerContext(value.Caller); err != nil {
		return errors.New("HostInstallation identity is invalid")
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("encode HostInstallation credential")
	}
	if len(body) > maxCredentialFileBytes {
		return errors.New("HostInstallation credential exceeds the safe size limit")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create HostInstallation configuration directory: %w", err)
	}
	if err := rejectUnsafeExistingCredential(path, replace); err != nil {
		return err
	}
	if !replace {
		return writeCredentialExclusive(path, append(body, '\n'))
	}
	return replaceCredentialAtomically(directory, path, append(body, '\n'))
}

func rejectUnsafeExistingCredential(path string, replace bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect HostInstallation credential file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("HostInstallation credential file must be a regular non-symlink file")
	}
	if err := validateCredentialPermissions(path, info); err != nil {
		return err
	}
	if !replace {
		return errors.New("HostInstallation credential already exists; pass --replace to replace it")
	}
	return nil
}

func writeCredentialExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("HostInstallation credential already exists; pass --replace to replace it")
	}
	if err != nil {
		return fmt.Errorf("create HostInstallation credential file: %w", err)
	}
	if err := writeAndCloseCredential(file, body); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func replaceCredentialAtomically(directory, path string, body []byte) error {
	temporary, err := os.CreateTemp(directory, ".host-installation-*.tmp")
	if err != nil {
		return fmt.Errorf("create HostInstallation credential replacement: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := secureCredentialFile(temporary); err != nil {
		return errors.New("secure HostInstallation credential replacement")
	}
	if err := writeAndCloseCredential(temporary, body); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit HostInstallation credential replacement: %w", err)
	}
	committed = true
	return nil
}

func writeAndCloseCredential(file *os.File, body []byte) error {
	if err := secureCredentialFile(file); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return errors.New("persist HostInstallation credential")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("sync HostInstallation credential")
	}
	if err := file.Close(); err != nil {
		return errors.New("close HostInstallation credential")
	}
	return nil
}

// EnsureLocalCredential creates one private, persistent Codex installation
// credential for the user-owned loopback profile. It is intentionally invalid
// for Team/SIT because those deployments require a server-side credential
// store, revocation and browser/device authorization.
func EnsureLocalCredential(path, serverURL string) (CredentialBundle, error) {
	if existing, err := LoadCredentialBundle(path); err == nil {
		return ensureExpectedServer(path, existing, serverURL)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CredentialBundle{}, err
	}
	credentialBytes := make([]byte, 32)
	installationBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, credentialBytes); err != nil {
		return CredentialBundle{}, errors.New("generate HostInstallation credential")
	}
	if _, err := io.ReadFull(rand.Reader, installationBytes); err != nil {
		return CredentialBundle{}, errors.New("generate HostInstallation identity")
	}
	value := CredentialBundle{
		ServerURL:  serverURL,
		Credential: hex.EncodeToString(credentialBytes),
		Caller: protocol.CallerContext{
			PrincipalID: "local-owner", SpaceID: "personal", HostInstallationID: "host-codex-" + hex.EncodeToString(installationBytes), HostKind: protocol.HostKindCodex,
		},
	}
	if err := protocol.ValidateCallerContext(value.Caller); err != nil {
		return CredentialBundle{}, err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return CredentialBundle{}, errors.New("encode HostInstallation credential")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return CredentialBundle{}, fmt.Errorf("create HostInstallation configuration directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, loadErr := LoadCredentialBundle(path)
		if loadErr != nil {
			return CredentialBundle{}, loadErr
		}
		return ensureExpectedServer(path, existing, serverURL)
	}
	if err != nil {
		return CredentialBundle{}, fmt.Errorf("create HostInstallation credential file: %w", err)
	}
	writeErr := error(nil)
	if err := secureCredentialFile(file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return CredentialBundle{}, err
	}
	if _, err := file.Write(append(body, '\n')); err != nil {
		writeErr = err
	}
	if err := file.Sync(); err != nil && writeErr == nil {
		writeErr = err
	}
	if err := file.Close(); err != nil && writeErr == nil {
		writeErr = err
	}
	if writeErr != nil {
		_ = os.Remove(path)
		return CredentialBundle{}, errors.New("persist HostInstallation credential")
	}
	return value, nil
}

func ensureExpectedServer(path string, value CredentialBundle, expected string) (CredentialBundle, error) {
	if strings.TrimSpace(value.ServerURL) != strings.TrimSpace(expected) {
		value.ServerURL = strings.TrimSpace(expected)
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return CredentialBundle{}, errors.New("encode HostInstallation endpoint update")
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), ".host-installation-*.tmp")
		if err != nil {
			return CredentialBundle{}, fmt.Errorf("create HostInstallation endpoint update: %w", err)
		}
		temporaryPath := temporary.Name()
		committed := false
		defer func() {
			_ = temporary.Close()
			if !committed {
				_ = os.Remove(temporaryPath)
			}
		}()
		if err := secureCredentialFile(temporary); err != nil {
			return CredentialBundle{}, errors.New("secure HostInstallation endpoint update")
		}
		if _, err := temporary.Write(append(body, '\n')); err != nil {
			return CredentialBundle{}, errors.New("write HostInstallation endpoint update")
		}
		if err := temporary.Sync(); err != nil {
			return CredentialBundle{}, errors.New("sync HostInstallation endpoint update")
		}
		if err := temporary.Close(); err != nil {
			return CredentialBundle{}, errors.New("close HostInstallation endpoint update")
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return CredentialBundle{}, fmt.Errorf("commit HostInstallation endpoint update: %w", err)
		}
		committed = true
	}
	return value, nil
}

type StaticAuthenticator struct {
	credentialHash [sha256.Size]byte
	caller         protocol.CallerContext
}

func NewStaticAuthenticator(value CredentialBundle) (*StaticAuthenticator, error) {
	if strings.TrimSpace(value.Credential) == "" {
		return nil, errors.New("HostInstallation credential is required")
	}
	if err := protocol.ValidateCallerContext(value.Caller); err != nil {
		return nil, err
	}
	return &StaticAuthenticator{credentialHash: sha256.Sum256([]byte(value.Credential)), caller: value.Caller}, nil
}

func (auth *StaticAuthenticator) AuthenticateHost(_ context.Context, credential string) (protocol.CallerContext, error) {
	if auth == nil || strings.TrimSpace(credential) == "" {
		return protocol.CallerContext{}, protocol.NewError(protocol.ErrorCodeAuthenticationRequired, "Host authentication is invalid or expired", false, true, "")
	}
	presented := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(presented[:], auth.credentialHash[:]) != 1 {
		return protocol.CallerContext{}, protocol.NewError(protocol.ErrorCodeAuthenticationRequired, "Host authentication is invalid or expired", false, true, "")
	}
	return auth.caller, nil
}
