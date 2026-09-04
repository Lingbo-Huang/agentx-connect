package hostinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	AgentXServerName      = "agentx"
	maximumMCPConfigBytes = 1 << 20
	ToolProfileFull       = "full"
	ToolProfileCompact    = "compact"
)

// JSONMCPConfigSpec describes a Host whose supported MCP installation surface
// is an mcp.json file with a top-level mcpServers object.
type JSONMCPConfigSpec struct {
	ConfigFile     string
	BridgeCommand  string
	CredentialFile string
	Profile        string
}

// JSONMCPConfigManager owns only the AgentX entry. It preserves all unrelated
// top-level values and servers and refuses to overwrite a conflicting entry.
type JSONMCPConfigManager struct {
	hostLabel string
}

func newJSONMCPConfigManager(hostLabel string) *JSONMCPConfigManager {
	hostLabel = strings.TrimSpace(hostLabel)
	if hostLabel == "" {
		hostLabel = "Host"
	}
	return &JSONMCPConfigManager{hostLabel: hostLabel}
}

func (manager *JSONMCPConfigManager) Check(spec JSONMCPConfigSpec) (InstallationState, error) {
	normalized, err := normalizeJSONMCPConfigSpec(manager.hostLabel, spec)
	if err != nil {
		return "", err
	}
	root, exists, err := loadJSONMCPConfig(manager.hostLabel, normalized.ConfigFile)
	if err != nil {
		return InstallationBroken, err
	}
	if !exists {
		return InstallationMissing, nil
	}
	servers, err := jsonMCPServers(manager.hostLabel, root)
	if err != nil {
		return InstallationBroken, err
	}
	entry, exists := servers[AgentXServerName]
	if !exists {
		return InstallationMissing, nil
	}
	if !matchesJSONMCPEntry(entry, normalized) {
		return InstallationConflict, nil
	}
	if err := validateJSONMCPConfigFiles(normalized); err != nil {
		return InstallationBroken, nil
	}
	return InstallationReady, nil
}

func (manager *JSONMCPConfigManager) Install(spec JSONMCPConfigSpec) (InstallationState, error) {
	normalized, err := normalizeJSONMCPConfigSpec(manager.hostLabel, spec)
	if err != nil {
		return "", err
	}
	state, err := manager.Check(normalized)
	if err != nil || state == InstallationReady {
		return state, err
	}
	if state == InstallationConflict {
		return state, fmt.Errorf("%s already has a different MCP server named agentx; review it before installing", manager.hostLabel)
	}
	if state == InstallationBroken {
		return state, fmt.Errorf("%s AgentX MCP configuration is malformed or references unavailable files; repair or remove it before installing", manager.hostLabel)
	}
	if err := validateJSONMCPConfigFiles(normalized); err != nil {
		return "", err
	}
	root, exists, err := loadJSONMCPConfig(manager.hostLabel, normalized.ConfigFile)
	if err != nil {
		return InstallationBroken, err
	}
	if !exists {
		root = make(map[string]json.RawMessage)
	}
	servers, err := jsonMCPServers(manager.hostLabel, root)
	if err != nil {
		return InstallationBroken, err
	}
	entry, err := json.Marshal(jsonMCPServer{
		Type: "stdio", Command: normalized.BridgeCommand,
		Arguments: []string{"--profile", normalized.Profile, "--credential-file", normalized.CredentialFile},
	})
	if err != nil {
		return "", fmt.Errorf("encode %s AgentX MCP entry", manager.hostLabel)
	}
	servers[AgentXServerName] = entry
	if err := setJSONMCPServers(root, servers); err != nil {
		return "", err
	}
	if err := saveJSONMCPConfig(manager.hostLabel, normalized.ConfigFile, root); err != nil {
		return "", err
	}
	state, err = manager.Check(normalized)
	if err != nil {
		return state, err
	}
	if state != InstallationReady {
		return state, fmt.Errorf("%s did not retain the expected AgentX MCP configuration", manager.hostLabel)
	}
	return state, nil
}

func (manager *JSONMCPConfigManager) Uninstall(spec JSONMCPConfigSpec) (InstallationState, error) {
	normalized, err := normalizeJSONMCPConfigSpec(manager.hostLabel, spec)
	if err != nil {
		return "", err
	}
	state, err := manager.Check(normalized)
	if err != nil || state == InstallationMissing {
		return state, err
	}
	if state == InstallationConflict {
		return state, fmt.Errorf("refusing to remove a different %s MCP server named agentx", manager.hostLabel)
	}
	root, _, err := loadJSONMCPConfig(manager.hostLabel, normalized.ConfigFile)
	if err != nil {
		return InstallationBroken, err
	}
	servers, err := jsonMCPServers(manager.hostLabel, root)
	if err != nil {
		return InstallationBroken, err
	}
	delete(servers, AgentXServerName)
	if err := setJSONMCPServers(root, servers); err != nil {
		return "", err
	}
	if err := saveJSONMCPConfig(manager.hostLabel, normalized.ConfigFile, root); err != nil {
		return "", err
	}
	state, err = manager.Check(normalized)
	if err != nil {
		return state, err
	}
	if state != InstallationMissing {
		return state, fmt.Errorf("%s retained the AgentX MCP configuration after removal", manager.hostLabel)
	}
	return state, nil
}

type jsonMCPServer struct {
	Type      string            `json:"type,omitempty"`
	Command   string            `json:"command"`
	Arguments []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

func normalizeJSONMCPConfigSpec(hostLabel string, spec JSONMCPConfigSpec) (JSONMCPConfigSpec, error) {
	spec.Profile = strings.ToLower(strings.TrimSpace(spec.Profile))
	if spec.Profile == "" {
		spec.Profile = ToolProfileCompact
	}
	if spec.Profile != ToolProfileFull && spec.Profile != ToolProfileCompact {
		return JSONMCPConfigSpec{}, fmt.Errorf("%s AgentX MCP profile must be full or compact", hostLabel)
	}
	var err error
	if spec.ConfigFile, err = absolutePath(spec.ConfigFile, hostLabel+" MCP configuration file"); err != nil {
		return JSONMCPConfigSpec{}, err
	}
	if spec.BridgeCommand, err = absolutePath(spec.BridgeCommand, "AgentX MCP command"); err != nil {
		return JSONMCPConfigSpec{}, err
	}
	if spec.CredentialFile, err = absolutePath(spec.CredentialFile, "HostInstallation credential file"); err != nil {
		return JSONMCPConfigSpec{}, err
	}
	return spec, nil
}

func validateJSONMCPConfigFiles(spec JSONMCPConfigSpec) error {
	if err := validateRegularFile(spec.BridgeCommand, "AgentX MCP command", true); err != nil {
		return err
	}
	return validateRegularFile(spec.CredentialFile, "HostInstallation credential file", false)
}

func loadJSONMCPConfig(hostLabel, path string) (map[string]json.RawMessage, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s MCP configuration: %w", hostLabel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s MCP configuration must be a regular non-symlink file", hostLabel)
	}
	if info.Size() > maximumMCPConfigBytes {
		return nil, false, fmt.Errorf("%s MCP configuration exceeds the safe size limit", hostLabel)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open %s MCP configuration: %w", hostLabel, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maximumMCPConfigBytes+1))
	root := make(map[string]json.RawMessage)
	if err := decoder.Decode(&root); err != nil {
		return nil, false, fmt.Errorf("%s MCP configuration is not valid JSON", hostLabel)
	}
	if root == nil {
		return nil, false, fmt.Errorf("%s MCP configuration must contain one JSON object", hostLabel)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, false, fmt.Errorf("%s MCP configuration must contain one JSON object", hostLabel)
	}
	return root, true, nil
}

func jsonMCPServers(hostLabel string, root map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	raw, exists := root["mcpServers"]
	if !exists {
		return make(map[string]json.RawMessage), nil
	}
	servers := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&servers); err != nil || servers == nil {
		return nil, fmt.Errorf("%s mcpServers must be a JSON object", hostLabel)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s mcpServers must contain one JSON object", hostLabel)
	}
	return servers, nil
}

func setJSONMCPServers(root map[string]json.RawMessage, servers map[string]json.RawMessage) error {
	encoded, err := json.Marshal(servers)
	if err != nil {
		return errors.New("encode MCP servers")
	}
	root["mcpServers"] = encoded
	return nil
}

func matchesJSONMCPEntry(raw json.RawMessage, spec JSONMCPConfigSpec) bool {
	var entry map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&entry); err != nil || entry == nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false
	}
	for key := range entry {
		switch key {
		case "type", "command", "args", "env":
		default:
			return false
		}
	}
	var server jsonMCPServer
	if err := json.Unmarshal(raw, &server); err != nil {
		return false
	}
	if server.Type != "" && server.Type != "stdio" {
		return false
	}
	return server.Command == spec.BridgeCommand &&
		equalStrings(server.Arguments, []string{"--profile", spec.Profile, "--credential-file", spec.CredentialFile}) && len(server.Env) == 0
}

func saveJSONMCPConfig(hostLabel, path string, root map[string]json.RawMessage) error {
	body, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s MCP configuration", hostLabel)
	}
	body = append(body, '\n')
	if len(body) > maximumMCPConfigBytes {
		return fmt.Errorf("%s MCP configuration exceeds the safe size limit", hostLabel)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create %s configuration directory: %w", hostLabel, err)
	}
	temporary, err := os.CreateTemp(directory, ".agentx-mcp-*.tmp")
	if err != nil {
		return fmt.Errorf("create %s MCP configuration replacement: %w", hostLabel, err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure %s MCP configuration replacement", hostLabel)
	}
	if _, err := temporary.Write(body); err != nil {
		return fmt.Errorf("write %s MCP configuration replacement", hostLabel)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync %s MCP configuration replacement", hostLabel)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s MCP configuration replacement", hostLabel)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit %s MCP configuration replacement: %w", hostLabel, err)
	}
	committed = true
	return nil
}
