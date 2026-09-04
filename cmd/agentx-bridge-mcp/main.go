// Command agentx-bridge-mcp exposes AgentX capabilities to Codex, Claude and
// other MCP Hosts over stdio. Stdout is reserved exclusively for MCP frames;
// diagnostics and startup errors are written to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostauth"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostconformance"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/hostinstall"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/httptransport"
	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/mcpserver"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "agentx-bridge-mcp:", err)
		os.Exit(1)
	}
}

func run(arguments []string, output, errorOutput io.Writer) error {
	return runWithDependencies(arguments, output, errorOutput, commandDependencies{
		probeMCP:    hostconformance.ProbeCommand,
		openBrowser: openBrowserURL,
	})
}

type commandDependencies struct {
	probeMCP    func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error)
	openBrowser func(context.Context, string) error
}

func runWithDependencies(arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	if len(arguments) > 0 && arguments[0] == "version" {
		if len(arguments) != 1 {
			return errors.New("version does not accept arguments")
		}
		_, err := fmt.Fprintf(output, "agentx-bridge-mcp %s (%s)\n", buildVersion, buildCommit)
		return err
	}
	if dependencies.probeMCP == nil {
		return errors.New("MCP process conformance dependency is required")
	}
	if dependencies.openBrowser == nil {
		return errors.New("browser opener dependency is required")
	}
	if len(arguments) >= 2 && (arguments[0] == "connect" || arguments[0] == "login" || arguments[0] == "install" || arguments[0] == "check" || arguments[0] == "doctor" || arguments[0] == "uninstall") {
		return runHostManagement(arguments, output, errorOutput, dependencies)
	}
	return runMCP(arguments, errorOutput)
}

func runMCP(arguments []string, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("agentx-bridge-mcp", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	hostName := flags.String("host", "codex", "Host identity used to select an independent AgentX HostInstallation credential")
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", ""), "path to the AgentX HostInstallation credential file")
	profile := flags.String("profile", string(mcpserver.ToolProfileFull), "MCP tool profile: full or compact")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	target, err := parseHostTarget(*hostName)
	if err != nil {
		return err
	}
	resolvedCredentialPath := strings.TrimSpace(*credentialPath)
	if resolvedCredentialPath == "" {
		resolvedCredentialPath, err = defaultCredentialPath(target.kind)
		if err != nil {
			return err
		}
	}
	credential, err := hostauth.LoadCredentialBundle(resolvedCredentialPath)
	if err != nil {
		return err
	}
	client, err := httptransport.NewClient(httptransport.ClientConfig{ServerURL: credential.ServerURL, Credential: credential.Credential})
	if err != nil {
		return fmt.Errorf("configure AgentX Server client: %w", err)
	}
	server, err := mcpserver.New(mcpserver.Config{
		Caller: credential.Caller, Search: client, Invoke: client, Handoff: client,
		Profile: mcpserver.ToolProfile(strings.ToLower(strings.TrimSpace(*profile))),
	})
	if err != nil {
		return fmt.Errorf("configure MCP server: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve MCP over stdio: %w", err)
	}
	return nil
}

func runHostManagement(arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	operation := arguments[0]
	target, err := parseHostTarget(arguments[1])
	if err != nil {
		return err
	}
	if operation == "connect" {
		return runHostConnect(target, arguments[2:], output, errorOutput, dependencies)
	}
	if operation == "login" {
		return runDeviceLogin(target, arguments[2:], output, errorOutput, dependencies.openBrowser)
	}
	switch target.kind {
	case protocol.HostKindCodex:
		return runCodexManagement(operation, arguments[2:], output, errorOutput, dependencies)
	case protocol.HostKindClaude:
		return runClaudeManagement(operation, arguments[2:], output, errorOutput, dependencies)
	case protocol.HostKindCursor:
		return runCursorManagement(operation, arguments[2:], output, errorOutput, dependencies)
	case protocol.HostKindWorkBuddy:
		return runWorkBuddyManagement(operation, arguments[2:], output, errorOutput, dependencies)
	case protocol.HostKindTrae:
		return runTraeManagement(operation, arguments[2:], output, errorOutput, dependencies)
	case protocol.HostKindSeal:
		return runSealManagement(operation, arguments[2:], output, errorOutput, dependencies)
	default:
		return errors.New("the requested Host does not have an installer in this build")
	}
}

type hostTarget struct {
	name string
	kind protocol.HostKind
}

const (
	hostConnectionReady             = "READY"
	hostConnectionReadyForHostTrust = "READY_FOR_HOST_TRUST"
	hostConnectionReadyForPlugin    = "READY_FOR_HOST_PLUGIN_INSTALL"
	workBuddyHostTrustInstruction   = "open WorkBuddy custom connectors and Trust agentx"
	sealHostPluginInstruction       = "install and enable the version-matched AgentX Agent Plugin in self-managed OpenClaw, or publish the AgentX remote Connector to the Seal/Lobi Gateway"
)

func parseHostTarget(value string) (hostTarget, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return hostTarget{name: "codex", kind: protocol.HostKindCodex}, nil
	case "claude", "claude-code":
		return hostTarget{name: "claude", kind: protocol.HostKindClaude}, nil
	case "cursor":
		return hostTarget{name: "cursor", kind: protocol.HostKindCursor}, nil
	case "workbuddy":
		return hostTarget{name: "workbuddy", kind: protocol.HostKindWorkBuddy}, nil
	case "trae":
		return hostTarget{name: "trae", kind: protocol.HostKindTrae}, nil
	case "seal":
		return hostTarget{name: "seal", kind: protocol.HostKindSeal}, nil
	case "openclaw":
		return hostTarget{name: "openclaw", kind: protocol.HostKindSeal}, nil
	case "lobi":
		return hostTarget{name: "lobi", kind: protocol.HostKindSeal}, nil
	default:
		return hostTarget{}, errors.New("supported Hosts are codex, claude, cursor, workbuddy, trae, seal, lobi, and openclaw")
	}
}

func runHostConnect(target hostTarget, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet("connect "+target.name, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	credentialDefault, err := defaultCredentialPath(target.kind)
	if err != nil {
		return err
	}
	bridgeDefault, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	profileDefault := hostinstall.ToolProfileCompact
	if target.kind == protocol.HostKindCodex {
		profileDefault = hostinstall.ToolProfileFull
	}
	configDefault := ""
	switch target.kind {
	case protocol.HostKindCursor:
		configDefault, err = defaultCursorConfigPath()
	case protocol.HostKindWorkBuddy:
		configDefault, err = defaultWorkBuddyConfigPath()
	case protocol.HostKindTrae:
		configDefault, err = defaultTraeConfigPath()
	}
	if err != nil {
		return err
	}
	serverURL := flags.String("server", envOr("AGENTX_SERVER_URL", ""), "AgentX Server HTTPS origin")
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", credentialDefault), "path to the AgentX HostInstallation credential file")
	displayName := flags.String("display-name", defaultDisplayName(target), "name shown in AgentX for this Host installation")
	replace := flags.Bool("replace", false, "replace an incompatible or expired local credential after successful authorization")
	noOpenBrowser := flags.Bool("no-open-browser", false, "print the Device Authorization URL without opening a browser")
	bridgePath := flags.String("bridge-command", bridgeDefault, "path to the agentx-bridge-mcp executable")
	profile := flags.String("profile", profileDefault, "MCP tool profile: compact or full")
	configHelp := "Host global or project mcp.json path"
	if target.kind == protocol.HostKindWorkBuddy {
		configHelp = "WorkBuddy user mcp.json path; alternate paths are for custom user-data roots and testing, not project scope"
	}
	configPath := flags.String("config-file", configDefault, configHelp)
	scope := flags.String("scope", hostinstall.ClaudeScopeUser, "Claude Code MCP scope: local, project, or user")
	codexCommand := flags.String("codex-command", "codex", "Codex CLI command or absolute path")
	claudeCommand := flags.String("claude-command", "claude", "Claude Code CLI command or absolute path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(*serverURL) == "" {
		return errors.New("AgentX Server URL is required; pass --server or AGENTX_SERVER_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reused, err := reusableHostCredential(*credentialPath, *serverURL, target.kind)
	if err != nil && !*replace {
		return fmt.Errorf("existing Host credential cannot be reused: %w; review it and rerun with --replace", err)
	}
	if !reused {
		if err := performDeviceLogin(ctx, target, deviceLoginConfig{
			serverURL: *serverURL, credentialPath: *credentialPath, displayName: *displayName,
			replace: *replace, openBrowser: !*noOpenBrowser,
		}, output, errorOutput, dependencies.openBrowser); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(output, "AgentX %s credential: REUSED\n", target.name); err != nil {
		return err
	}
	managementArguments := []string{"--credential-file", *credentialPath, "--bridge-command", *bridgePath, "--profile", *profile}
	switch target.kind {
	case protocol.HostKindCodex:
		managementArguments = append(managementArguments, "--codex-command", *codexCommand)
	case protocol.HostKindClaude:
		managementArguments = append(managementArguments, "--claude-command", *claudeCommand, "--scope", *scope)
	case protocol.HostKindCursor, protocol.HostKindWorkBuddy, protocol.HostKindTrae:
		managementArguments = append(managementArguments, "--config-file", *configPath)
	}
	if target.kind == protocol.HostKindSeal {
		if err := runHostManagement(append([]string{"doctor", target.name}, managementArguments...), output, errorOutput, dependencies); err != nil {
			return fmt.Errorf("Host credential is authorized but local Bridge diagnostics failed: %w", err)
		}
		_, err = fmt.Fprintf(output, "AgentX %s connection: %s\n", target.name, hostConnectionReadyForPlugin)
		return err
	}
	if err := runHostManagement(append([]string{"install", target.name}, managementArguments...), output, errorOutput, dependencies); err != nil {
		return fmt.Errorf("Host credential is authorized but MCP installation failed: %w", err)
	}
	if err := runHostManagement(append([]string{"doctor", target.name}, managementArguments...), output, errorOutput, dependencies); err != nil {
		return fmt.Errorf("Host MCP is installed but connection diagnostics failed: %w", err)
	}
	connectionState := hostConnectionReady
	if target.kind == protocol.HostKindWorkBuddy {
		connectionState = hostConnectionReadyForHostTrust
	}
	_, err = fmt.Fprintf(output, "AgentX %s connection: %s\n", target.name, connectionState)
	return err
}

func runClaudeManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet(operation+" claude", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultPath, err := defaultCredentialPath(protocol.HostKindClaude)
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	claudeCommand := flags.String("claude-command", "claude", "Claude Code CLI command or absolute path")
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultPath), "path to the AgentX HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	profile := flags.String("profile", hostinstall.ToolProfileCompact, "MCP tool profile: compact or full")
	scope := flags.String("scope", hostinstall.ClaudeScopeUser, "Claude Code MCP scope: local, project, or user")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	manager, err := hostinstall.NewClaudeManager(hostinstall.ExecRunner{})
	if err != nil {
		return err
	}
	spec := hostinstall.ClaudeSpec{
		ClaudeCommand: *claudeCommand, BridgeCommand: *bridgePath, CredentialFile: *credentialPath,
		Profile: *profile, Scope: *scope,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if operation == "doctor" {
		return runClaudeDoctor(ctx, manager, spec, *credentialPath, output, dependencies.probeMCP)
	}
	var state hostinstall.InstallationState
	switch operation {
	case "install":
		state, err = manager.Install(ctx, spec)
	case "check":
		state, err = manager.Check(ctx, spec)
	case "uninstall":
		state, err = manager.Uninstall(ctx, spec)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "AgentX Claude Code MCP: %s\n", state)
	return err
}

func runCursorManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet(operation+" cursor", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultCredential, err := defaultCredentialPath(protocol.HostKindCursor)
	if err != nil {
		return err
	}
	defaultConfig, err := defaultCursorConfigPath()
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultCredential), "path to the AgentX HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	configPath := flags.String("config-file", defaultConfig, "global or project-level Cursor mcp.json path")
	profile := flags.String("profile", hostinstall.CursorProfileCompact, "MCP tool profile: compact or full")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	spec := hostinstall.CursorSpec{
		ConfigFile: *configPath, BridgeCommand: *bridgePath, CredentialFile: *credentialPath, Profile: *profile,
	}
	manager := hostinstall.NewCursorManager()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if operation == "doctor" {
		return runCursorDoctor(ctx, manager, spec, *credentialPath, output, dependencies.probeMCP)
	}
	var state hostinstall.InstallationState
	switch operation {
	case "install":
		state, err = manager.Install(spec)
	case "check":
		state, err = manager.Check(spec)
	case "uninstall":
		state, err = manager.Uninstall(spec)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "AgentX Cursor MCP: %s\n", state)
	return err
}

func runCodexManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet(operation+" codex", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultPath, err := defaultCredentialPath(protocol.HostKindCodex)
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	codexCommand := flags.String("codex-command", "codex", "Codex CLI command or absolute path")
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultPath), "path to the AgentX HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	profile := flags.String("profile", hostinstall.ToolProfileFull, "MCP tool profile: full or compact")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	manager, err := hostinstall.NewCodexManager(hostinstall.ExecRunner{})
	if err != nil {
		return err
	}
	spec := hostinstall.CodexSpec{CodexCommand: *codexCommand, BridgeCommand: *bridgePath, CredentialFile: *credentialPath, Profile: *profile}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if operation == "doctor" {
		return runCodexDoctor(ctx, manager, spec, *credentialPath, output, dependencies.probeMCP)
	}
	var state hostinstall.InstallationState
	switch operation {
	case "install":
		state, err = manager.Install(ctx, spec)
	case "check":
		state, err = manager.Check(ctx, spec)
	case "uninstall":
		state, err = manager.Uninstall(ctx, spec)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "AgentX Codex MCP: %s\n", state)
	return err
}

func runWorkBuddyManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet(operation+" workbuddy", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultCredential, err := defaultCredentialPath(protocol.HostKindWorkBuddy)
	if err != nil {
		return err
	}
	defaultConfig, err := defaultWorkBuddyConfigPath()
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultCredential), "path to the AgentX HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	configPath := flags.String("config-file", defaultConfig, "WorkBuddy user mcp.json path; alternate paths are for custom user-data roots and testing, not project scope")
	profile := flags.String("profile", hostinstall.WorkBuddyProfileCompact, "MCP tool profile: compact or full")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	spec := hostinstall.WorkBuddySpec{
		ConfigFile: *configPath, BridgeCommand: *bridgePath, CredentialFile: *credentialPath, Profile: *profile,
	}
	manager := hostinstall.NewWorkBuddyManager()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if operation == "doctor" {
		return runWorkBuddyDoctor(ctx, manager, spec, *credentialPath, output, dependencies.probeMCP)
	}
	var state hostinstall.InstallationState
	switch operation {
	case "install":
		state, err = manager.Install(spec)
	case "check":
		state, err = manager.Check(spec)
	case "uninstall":
		state, err = manager.Uninstall(spec)
	}
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(output, "AgentX WorkBuddy MCP: %s\n", state); err != nil {
		return err
	}
	if operation != "uninstall" && state == hostinstall.InstallationReady {
		return writeWorkBuddyHostActivation(output)
	}
	return nil
}

func runTraeManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	flags := flag.NewFlagSet(operation+" trae", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultCredential, err := defaultCredentialPath(protocol.HostKindTrae)
	if err != nil {
		return err
	}
	defaultConfig, err := defaultTraeConfigPath()
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultCredential), "path to the AgentX HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	configPath := flags.String("config-file", defaultConfig, "project-level Trae mcp.json path")
	profile := flags.String("profile", hostinstall.TraeProfileCompact, "MCP tool profile: compact or full")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	spec := hostinstall.TraeSpec{
		ConfigFile: *configPath, BridgeCommand: *bridgePath, CredentialFile: *credentialPath, Profile: *profile,
	}
	manager := hostinstall.NewTraeManager()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if operation == "doctor" {
		return runTraeDoctor(ctx, manager, spec, *credentialPath, output, dependencies.probeMCP)
	}
	var state hostinstall.InstallationState
	switch operation {
	case "install":
		state, err = manager.Install(spec)
	case "check":
		state, err = manager.Check(spec)
	case "uninstall":
		state, err = manager.Uninstall(spec)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "AgentX Trae MCP: %s\n", state)
	return err
}

func runSealManagement(operation string, arguments []string, output, errorOutput io.Writer, dependencies commandDependencies) error {
	if operation != "doctor" {
		return errors.New("AgentX CLI does not install or remove Seal/OpenClaw/Lobi plugins; use the reviewed Host plugin or Skill management flow")
	}
	flags := flag.NewFlagSet(operation+" seal", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultCredential, err := defaultCredentialPath(protocol.HostKindSeal)
	if err != nil {
		return err
	}
	bridgeCommand, err := os.Executable()
	if err != nil {
		return errors.New("resolve AgentX MCP executable")
	}
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultCredential), "path to the AgentX Seal/OpenClaw HostInstallation credential file")
	bridgePath := flags.String("bridge-command", bridgeCommand, "path to the agentx-bridge-mcp executable")
	profile := flags.String("profile", hostinstall.ToolProfileCompact, "MCP tool profile: compact or full")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := runHostDoctor(ctx, "Seal/OpenClaw local Bridge", protocol.HostKindSeal, func() (hostinstall.InstallationState, error) {
		return hostinstall.InstallationReady, nil
	}, *credentialPath, *bridgePath, *profile, output, dependencies.probeMCP); err != nil {
		return err
	}
	return writeSealHostActivation(output)
}

func writeSealHostActivation(output io.Writer) error {
	_, err := fmt.Fprintf(output, "AgentX Seal/OpenClaw Host activation: USER_ACTION_REQUIRED (%s)\n", sealHostPluginInstruction)
	return err
}

type codexInstallationChecker interface {
	Check(context.Context, hostinstall.CodexSpec) (hostinstall.InstallationState, error)
}

func runCodexDoctor(
	ctx context.Context,
	manager codexInstallationChecker,
	spec hostinstall.CodexSpec,
	credentialPath string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	return runHostDoctor(ctx, "Codex", protocol.HostKindCodex, func() (hostinstall.InstallationState, error) {
		return manager.Check(ctx, spec)
	}, credentialPath, spec.BridgeCommand, spec.Profile, output, probe)
}

type workBuddyInstallationChecker interface {
	Check(hostinstall.WorkBuddySpec) (hostinstall.InstallationState, error)
}

type traeInstallationChecker interface {
	Check(hostinstall.TraeSpec) (hostinstall.InstallationState, error)
}

type claudeInstallationChecker interface {
	Check(context.Context, hostinstall.ClaudeSpec) (hostinstall.InstallationState, error)
}

func runClaudeDoctor(
	ctx context.Context,
	manager claudeInstallationChecker,
	spec hostinstall.ClaudeSpec,
	credentialPath string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	return runHostDoctor(ctx, "Claude Code", protocol.HostKindClaude, func() (hostinstall.InstallationState, error) {
		return manager.Check(ctx, spec)
	}, credentialPath, spec.BridgeCommand, spec.Profile, output, probe)
}

type cursorInstallationChecker interface {
	Check(hostinstall.CursorSpec) (hostinstall.InstallationState, error)
}

func runCursorDoctor(
	ctx context.Context,
	manager cursorInstallationChecker,
	spec hostinstall.CursorSpec,
	credentialPath string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	return runHostDoctor(ctx, "Cursor", protocol.HostKindCursor, func() (hostinstall.InstallationState, error) {
		return manager.Check(spec)
	}, credentialPath, spec.BridgeCommand, spec.Profile, output, probe)
}

func runWorkBuddyDoctor(
	ctx context.Context,
	manager workBuddyInstallationChecker,
	spec hostinstall.WorkBuddySpec,
	credentialPath string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	if err := runHostDoctor(ctx, "WorkBuddy", protocol.HostKindWorkBuddy, func() (hostinstall.InstallationState, error) {
		return manager.Check(spec)
	}, credentialPath, spec.BridgeCommand, spec.Profile, output, probe); err != nil {
		return err
	}
	return writeWorkBuddyHostActivation(output)
}

func writeWorkBuddyHostActivation(output io.Writer) error {
	_, err := fmt.Fprintf(output, "AgentX WorkBuddy Host activation: USER_ACTION_REQUIRED (%s)\n", workBuddyHostTrustInstruction)
	return err
}

func runTraeDoctor(
	ctx context.Context,
	manager traeInstallationChecker,
	spec hostinstall.TraeSpec,
	credentialPath string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	return runHostDoctor(ctx, "Trae", protocol.HostKindTrae, func() (hostinstall.InstallationState, error) {
		return manager.Check(spec)
	}, credentialPath, spec.BridgeCommand, spec.Profile, output, probe)
}

func runHostDoctor(
	ctx context.Context,
	hostName string,
	expectedKind protocol.HostKind,
	check func() (hostinstall.InstallationState, error),
	credentialPath string,
	bridgeCommand string,
	profile string,
	output io.Writer,
	probe func(context.Context, hostconformance.CommandSpec) (hostconformance.Evidence, error),
) error {
	if probe == nil {
		return errors.New("MCP process conformance dependency is required")
	}
	state, err := check()
	if err != nil {
		return fmt.Errorf("check %s MCP installation: %w", hostName, err)
	}
	if state != hostinstall.InstallationReady {
		return fmt.Errorf("%s MCP installation is %s; run install %s before doctor", hostName, state, strings.ToLower(hostName))
	}
	credential, err := hostauth.LoadCredentialBundle(credentialPath)
	if err != nil {
		return fmt.Errorf("load private HostInstallation credential: %w", err)
	}
	if credential.ExpiresAt != nil && !credential.ExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("HostInstallation credential expired; run login %s --replace", strings.ToLower(hostName))
	}
	if credential.Caller.HostKind != expectedKind {
		return fmt.Errorf("HostInstallation credential belongs to %s, want %s; run login %s --replace", credential.Caller.HostKind, expectedKind, strings.ToLower(hostName))
	}
	client, err := httptransport.NewClient(httptransport.ClientConfig{
		ServerURL: credential.ServerURL, Credential: credential.Credential,
	})
	if err != nil {
		return fmt.Errorf("configure AgentX Server diagnostic: %w", err)
	}
	response, err := client.Search(ctx, protocol.SearchCapabilitiesRequest{
		ContractVersion: protocol.ContractVersion, Caller: credential.Caller,
		Query: "AgentX connection diagnostic", MaximumSensitivity: protocol.SensitivityInternal,
		Mode: protocol.RoutingModeBalanced, Limit: 1,
	})
	if err != nil {
		return fmt.Errorf("authenticate and search AgentX Server: %w", err)
	}
	evidence, err := probe(ctx, hostconformance.CommandSpec{
		Executable: bridgeCommand, CredentialFile: credentialPath,
		Profile: mcpserver.ToolProfile(strings.ToLower(strings.TrimSpace(profile))),
	})
	if err != nil {
		return fmt.Errorf("validate %s MCP process: %w", hostName, err)
	}
	_, err = fmt.Fprintf(output,
		"AgentX %s MCP configuration: READY\nAgentX Host credential: READY\nAgentX Server authentication: READY\nAgentX capability search: READY (%d visible match)\nAgentX MCP process conformance: READY (%s profile, %d tools)\n",
		hostName, len(response.Matches), evidence.Profile, evidence.ToolCount,
	)
	return err
}

func runDeviceLogin(target hostTarget, arguments []string, output, errorOutput io.Writer, openBrowser func(context.Context, string) error) error {
	flags := flag.NewFlagSet("login "+target.name, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	defaultPath, err := defaultCredentialPath(target.kind)
	if err != nil {
		return err
	}
	serverURL := flags.String("server", envOr("AGENTX_SERVER_URL", ""), "AgentX Server HTTPS origin")
	credentialPath := flags.String("credential-file", envOr("AGENTX_HOST_CREDENTIAL_FILE", defaultPath), "path to the AgentX HostInstallation credential file")
	displayName := flags.String("display-name", defaultDisplayName(target), "name shown in AgentX for this Host installation")
	replace := flags.Bool("replace", false, "replace an existing local HostInstallation credential after successful authorization")
	reuse := flags.Bool("reuse", false, "reuse a compatible unexpired credential without registering native MCP")
	noOpenBrowser := flags.Bool("no-open-browser", false, "print the Device Authorization URL without opening a browser")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *reuse && *replace {
		return errors.New("--reuse and --replace cannot be combined")
	}
	if *reuse {
		ready, err := reusableHostCredential(*credentialPath, *serverURL, target.kind)
		if err != nil {
			return err
		}
		if ready {
			_, err := fmt.Fprintln(output, "AgentX Host credential reused; the Server rechecks authority on every tool call")
			return err
		}
	} else if _, err := os.Lstat(*credentialPath); err == nil {
		if _, err := hostauth.LoadCredentialBundle(*credentialPath); err != nil {
			return err
		}
		if !*replace {
			return errors.New("Host credential already exists; use --reuse or explicitly --replace")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect Host credential before authorization")
	}
	return performDeviceLogin(context.Background(), target, deviceLoginConfig{
		serverURL: *serverURL, credentialPath: *credentialPath, displayName: *displayName,
		replace: *replace, openBrowser: !*noOpenBrowser,
	}, output, errorOutput, openBrowser)
}

type deviceLoginConfig struct {
	serverURL      string
	credentialPath string
	displayName    string
	replace        bool
	openBrowser    bool
}

func performDeviceLogin(
	ctx context.Context,
	target hostTarget,
	config deviceLoginConfig,
	output io.Writer,
	warningOutput io.Writer,
	openBrowser func(context.Context, string) error,
) error {
	client, err := httptransport.NewDeviceClient(config.serverURL, nil)
	if err != nil {
		return fmt.Errorf("configure AgentX device authorization: %w", err)
	}
	started, err := client.Begin(ctx, target.kind, config.displayName)
	if err != nil {
		return fmt.Errorf("start AgentX device authorization: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Open %s\nEnter code: %s\nWaiting for approval...\n", started.VerificationURI, started.UserCode); err != nil {
		return errors.New("write AgentX device authorization instructions")
	}
	if config.openBrowser {
		if openBrowser == nil {
			return errors.New("browser opener dependency is required")
		}
		if err := openBrowser(ctx, started.VerificationURI); err != nil {
			if _, writeErr := fmt.Fprintln(warningOutput, "AgentX could not open the approval page automatically; open the printed URL in a browser."); writeErr != nil {
				return errors.New("write AgentX browser fallback instruction")
			}
		}
	}
	loginCtx, cancel := context.WithDeadline(ctx, started.ExpiresAt)
	defer cancel()
	token, err := waitForDeviceAuthorization(loginCtx, client, started.DeviceCode, time.Duration(started.IntervalSeconds)*time.Second)
	if err != nil {
		return err
	}
	expiresAt := token.ExpiresAt
	if err := hostauth.SaveCredentialBundle(config.credentialPath, hostauth.CredentialBundle{
		ServerURL: strings.TrimSpace(config.serverURL), Credential: token.Credential,
		Caller: token.Caller, ExpiresAt: &expiresAt,
	}, config.replace); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "AgentX %s sign-in complete: %s\n", target.name, token.Caller.HostInstallationID)
	return err
}

func openBrowserURL(ctx context.Context, rawURL string) error {
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(openCtx, "open", rawURL)
	case "windows":
		command = exec.CommandContext(openCtx, "rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		command = exec.CommandContext(openCtx, "xdg-open", rawURL)
	}
	if err := command.Run(); err != nil {
		return errors.New("start system browser")
	}
	return nil
}

func reusableHostCredential(path, serverURL string, expectedKind protocol.HostKind) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect Host credential: %w", err)
	}
	bundle, err := hostauth.LoadCredentialBundle(path)
	if err != nil {
		return false, err
	}
	if bundle.Caller.HostKind != expectedKind {
		return false, fmt.Errorf("credential belongs to %s, want %s", bundle.Caller.HostKind, expectedKind)
	}
	if strings.TrimRight(strings.TrimSpace(bundle.ServerURL), "/") != strings.TrimRight(strings.TrimSpace(serverURL), "/") {
		return false, errors.New("credential belongs to a different AgentX Server")
	}
	if bundle.ExpiresAt != nil && !bundle.ExpiresAt.After(time.Now().UTC()) {
		return false, errors.New("credential is expired")
	}
	return true, nil
}

func waitForDeviceAuthorization(ctx context.Context, client *httptransport.DeviceClient, deviceCode string, interval time.Duration) (hostauth.DeviceAuthorizationToken, error) {
	if interval < time.Second {
		interval = time.Second
	}
	for {
		token, err := client.Poll(ctx, deviceCode)
		if err == nil {
			return token, nil
		}
		switch {
		case errors.Is(err, hostauth.ErrAuthorizationPending):
		case errors.Is(err, hostauth.ErrAuthorizationSlowDown):
			interval += 5 * time.Second
			if interval > 30*time.Second {
				interval = 30 * time.Second
			}
		case errors.Is(err, hostauth.ErrAuthorizationDenied):
			return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX device authorization was denied")
		case errors.Is(err, hostauth.ErrAuthorizationExpired), errors.Is(err, context.DeadlineExceeded):
			return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX device authorization expired; run login again")
		case errors.Is(err, hostauth.ErrAuthorizationConsumed):
			return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX device authorization was already used; run login again")
		default:
			return hostauth.DeviceAuthorizationToken{}, fmt.Errorf("exchange AgentX device authorization: %w", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return hostauth.DeviceAuthorizationToken{}, errors.New("AgentX device authorization expired; run login again")
		case <-timer.C:
		}
	}
}

func defaultDisplayName(target hostTarget) string {
	label := map[protocol.HostKind]string{
		protocol.HostKindCodex: "Codex", protocol.HostKindClaude: "Claude Code",
		protocol.HostKindCursor: "Cursor", protocol.HostKindWorkBuddy: "WorkBuddy", protocol.HostKindTrae: "Trae", protocol.HostKindSeal: "Seal/OpenClaw",
	}[target.kind]
	if label == "" {
		label = "Agent"
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return label + " Host"
	}
	return label + " on " + strings.TrimSpace(hostname)
}

func defaultCredentialPath(kind protocol.HostKind) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("resolve user configuration directory")
	}
	name := "host-installation.json"
	switch kind {
	case protocol.HostKindClaude:
		name = "host-installation-claude.json"
	case protocol.HostKindCursor:
		name = "host-installation-cursor.json"
	case protocol.HostKindWorkBuddy:
		name = "host-installation-workbuddy.json"
	case protocol.HostKindTrae:
		name = "host-installation-trae.json"
	case protocol.HostKindSeal:
		name = "host-installation-seal.json"
	}
	return filepath.Join(configDir, "agentx", name), nil
}

func defaultCursorConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("resolve Cursor user configuration directory")
	}
	return filepath.Join(home, ".cursor", "mcp.json"), nil
}

func defaultWorkBuddyConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errors.New("resolve WorkBuddy user configuration directory")
	}
	return filepath.Join(home, ".workbuddy", "mcp.json"), nil
}

func defaultTraeConfigPath() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil || strings.TrimSpace(workingDirectory) == "" {
		return "", errors.New("resolve Trae project directory")
	}
	return filepath.Join(workingDirectory, ".trae", "mcp.json"), nil
}

func envOr(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
