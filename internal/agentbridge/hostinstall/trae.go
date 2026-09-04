package hostinstall

const (
	TraeServerName     = AgentXServerName
	TraeProfileFull    = ToolProfileFull
	TraeProfileCompact = ToolProfileCompact
)

type TraeSpec struct {
	ConfigFile     string
	BridgeCommand  string
	CredentialFile string
	Profile        string
}

type TraeManager struct {
	manager *JSONMCPConfigManager
}

func NewTraeManager() *TraeManager {
	return &TraeManager{manager: newJSONMCPConfigManager("Trae")}
}

func (manager *TraeManager) Check(spec TraeSpec) (InstallationState, error) {
	return manager.manager.Check(traeJSONSpec(spec))
}

func (manager *TraeManager) Install(spec TraeSpec) (InstallationState, error) {
	return manager.manager.Install(traeJSONSpec(spec))
}

func (manager *TraeManager) Uninstall(spec TraeSpec) (InstallationState, error) {
	return manager.manager.Uninstall(traeJSONSpec(spec))
}

func traeJSONSpec(spec TraeSpec) JSONMCPConfigSpec {
	return JSONMCPConfigSpec{
		ConfigFile: spec.ConfigFile, BridgeCommand: spec.BridgeCommand,
		CredentialFile: spec.CredentialFile, Profile: spec.Profile,
	}
}
