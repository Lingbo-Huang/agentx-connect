package hostinstall

import "encoding/json"

const (
	WorkBuddyServerName     = AgentXServerName
	WorkBuddyProfileFull    = ToolProfileFull
	WorkBuddyProfileCompact = ToolProfileCompact
)

type WorkBuddySpec struct {
	ConfigFile     string
	BridgeCommand  string
	CredentialFile string
	Profile        string
}

type WorkBuddyManager struct {
	manager *JSONMCPConfigManager
}

func NewWorkBuddyManager() *WorkBuddyManager {
	return &WorkBuddyManager{manager: newJSONMCPConfigManager("WorkBuddy")}
}

func (manager *WorkBuddyManager) Check(spec WorkBuddySpec) (InstallationState, error) {
	return manager.manager.Check(workBuddyJSONSpec(spec))
}

func (manager *WorkBuddyManager) Install(spec WorkBuddySpec) (InstallationState, error) {
	return manager.manager.Install(workBuddyJSONSpec(spec))
}

func (manager *WorkBuddyManager) Uninstall(spec WorkBuddySpec) (InstallationState, error) {
	return manager.manager.Uninstall(workBuddyJSONSpec(spec))
}

func workBuddyJSONSpec(spec WorkBuddySpec) JSONMCPConfigSpec {
	return JSONMCPConfigSpec{
		ConfigFile: spec.ConfigFile, BridgeCommand: spec.BridgeCommand,
		CredentialFile: spec.CredentialFile, Profile: spec.Profile,
	}
}

// These wrappers keep package tests readable while the parser and atomic
// writer are shared with other JSON-configured Hosts.
func loadWorkBuddyConfig(path string) (map[string]json.RawMessage, bool, error) {
	return loadJSONMCPConfig("WorkBuddy", path)
}

func workBuddyServers(root map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	return jsonMCPServers("WorkBuddy", root)
}
