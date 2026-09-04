// Package hostconformance verifies the MCP process contract exposed to an AI
// Host. It is deliberately read-only: conformance may list tools and search
// capabilities, but it must never invoke work or mutate a Mission.
package hostconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Lingbo-Huang/agentx-connect/internal/agentbridge/mcpserver"
	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
)

const (
	clientName    = "agentx-host-conformance"
	clientVersion = "0.1.0"
	searchQuery   = "AgentX Host MCP conformance diagnostic"
)

type Evidence struct {
	Profile    mcpserver.ToolProfile
	ToolCount  int
	MatchCount int
}

type CommandSpec struct {
	Executable     string
	CredentialFile string
	Profile        mcpserver.ToolProfile
}

// ProbeCommand starts the same stdio MCP command installed into a Host. The
// credential value stays in its private file and is never placed in argv.
func ProbeCommand(ctx context.Context, spec CommandSpec) (Evidence, error) {
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.CredentialFile) {
		return Evidence{}, errors.New("MCP process paths must be absolute")
	}
	if _, err := expectedTools(spec.Profile); err != nil {
		return Evidence{}, err
	}
	command := exec.CommandContext(ctx, spec.Executable,
		"--profile", string(spec.Profile), "--credential-file", spec.CredentialFile)
	// The Bridge is required to keep stdout exclusively for MCP frames. Stderr
	// may contain local paths, so Doctor reports a stable stage instead of
	// reflecting subprocess output into a Host or log.
	command.Stderr = io.Discard
	return Probe(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, spec.Profile)
}

func Probe(ctx context.Context, transport mcp.Transport, profile mcpserver.ToolProfile) (Evidence, error) {
	if transport == nil {
		return Evidence{}, errors.New("MCP conformance transport is required")
	}
	expected, err := expectedTools(profile)
	if err != nil {
		return Evidence{}, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return Evidence{}, errors.New("MCP process initialize failed")
	}
	defer session.Close()

	initialization := session.InitializeResult()
	if initialization == nil || !validInstructions(initialization.Instructions) {
		return Evidence{}, errors.New("MCP process instructions are incompatible")
	}
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return Evidence{}, errors.New("MCP process tool discovery failed")
	}
	if err := validateTools(listed.Tools, expected); err != nil {
		return Evidence{}, err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: string(protocol.ToolSearchCapabilities), Arguments: map[string]any{
		"query": searchQuery, "maximumSensitivity": string(protocol.SensitivityInternal), "mode": string(protocol.RoutingModeBalanced), "limit": float64(1),
	}})
	if err != nil || result == nil || result.IsError || result.StructuredContent == nil {
		return Evidence{}, errors.New("MCP process capability search failed")
	}
	payload, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return Evidence{}, errors.New("MCP process capability search returned invalid structured content")
	}
	var response protocol.SearchCapabilitiesResponse
	if json.Unmarshal(payload, &response) != nil || protocol.ValidateSearchCapabilitiesResponse(response) != nil {
		return Evidence{}, errors.New("MCP process capability search returned an invalid AgentX contract")
	}
	return Evidence{Profile: profile, ToolCount: len(listed.Tools), MatchCount: len(response.Matches)}, nil
}

func expectedTools(profile mcpserver.ToolProfile) ([]protocol.ToolName, error) {
	switch profile {
	case mcpserver.ToolProfileFull:
		return protocol.ToolSurface(), nil
	case mcpserver.ToolProfileCompact:
		return protocol.CompactToolSurface(), nil
	default:
		return nil, errors.New("MCP conformance profile is invalid")
	}
}

func validInstructions(value string) bool {
	for _, required := range []string{"PASS_THROUGH", "INVOKE", "HANDOFF", "HandoffRef", "Never silently fall back to a Runtime"} {
		if !strings.Contains(value, required) {
			return false
		}
	}
	return true
}

func validateTools(tools []*mcp.Tool, expected []protocol.ToolName) error {
	type annotationContract struct {
		readOnly  bool
		openWorld bool
	}
	wanted := make(map[string]annotationContract, len(expected))
	for _, name := range expected {
		switch name {
		case protocol.ToolSearchCapabilities, protocol.ToolListHandoffs,
			protocol.ToolGetHandoffStatus, protocol.ToolFetchArtifact:
			wanted[string(name)] = annotationContract{readOnly: true, openWorld: false}
		case protocol.ToolInvokeCapability, protocol.ToolHandoffWork, protocol.ToolUseCapability:
			wanted[string(name)] = annotationContract{readOnly: false, openWorld: true}
		case protocol.ToolAcceptDelivery, protocol.ToolRejectDelivery:
			wanted[string(name)] = annotationContract{readOnly: false, openWorld: false}
		default:
			return errors.New("MCP conformance tool contract is unknown")
		}
	}
	seen := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			return errors.New("MCP process returned an empty tool")
		}
		contract, ok := wanted[tool.Name]
		if !ok {
			return errors.New("MCP process exposed an unexpected tool")
		}
		if !objectSchema(tool.InputSchema) || !objectSchema(tool.OutputSchema) || tool.Annotations == nil ||
			!tool.Annotations.IdempotentHint || tool.Annotations.OpenWorldHint == nil ||
			tool.Annotations.ReadOnlyHint != contract.readOnly || *tool.Annotations.OpenWorldHint != contract.openWorld {
			return fmt.Errorf("MCP process tool %s has an incomplete schema or annotation", tool.Name)
		}
		if !contract.readOnly && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			return fmt.Errorf("MCP process tool %s does not declare additive non-destructive mutation", tool.Name)
		}
		seen = append(seen, tool.Name)
	}
	if len(seen) != len(wanted) {
		return errors.New("MCP process tool profile is incomplete")
	}
	sort.Strings(seen)
	for _, name := range seen {
		delete(wanted, name)
	}
	if len(wanted) != 0 {
		return errors.New("MCP process tool profile is incomplete")
	}
	return nil
}

func objectSchema(schema any) bool {
	value, ok := schema.(map[string]any)
	return ok && value["type"] == "object"
}
