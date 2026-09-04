package hostinstall

const (
	CursorServerName     = AgentXServerName
	CursorProfileFull    = ToolProfileFull
	CursorProfileCompact = ToolProfileCompact
)

type CursorSpec struct {
	ConfigFile     string
	BridgeCommand  string
	CredentialFile string
	Profile        string
}

type CursorManager struct {
	manager *JSONMCPConfigManager
}

func NewCursorManager() *CursorManager {
	return &CursorManager{manager: newJSONMCPConfigManager("Cursor")}
}

func (manager *CursorManager) Check(spec CursorSpec) (InstallationState, error) {
	return manager.manager.Check(cursorJSONSpec(spec))
}

func (manager *CursorManager) Install(spec CursorSpec) (InstallationState, error) {
	return manager.manager.Install(cursorJSONSpec(spec))
}

func (manager *CursorManager) Uninstall(spec CursorSpec) (InstallationState, error) {
	return manager.manager.Uninstall(cursorJSONSpec(spec))
}

func cursorJSONSpec(spec CursorSpec) JSONMCPConfigSpec {
	return JSONMCPConfigSpec{
		ConfigFile: spec.ConfigFile, BridgeCommand: spec.BridgeCommand,
		CredentialFile: spec.CredentialFile, Profile: spec.Profile,
	}
}
