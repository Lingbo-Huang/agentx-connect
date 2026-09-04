// AgentX's public installer manages only explicitly selected Host files.
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed SKILL.md
var skill []byte
var buildVersion = "dev"

const repository = "Lingbo-Huang/agentx-connect"

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)

type state struct {
	Version    string `json:"version"`
	Host       string `json:"host"`
	BinaryHash string `json:"binaryHash"`
	Source     string `json:"source,omitempty"`
	SkillHash  string `json:"skillHash,omitempty"`
	SkillPath  string `json:"skillPath,omitempty"`
}
type change struct {
	Path      string `json:"path"`
	Before    []byte `json:"before,omitempty"`
	Existed   bool   `json:"existed"`
	AfterHash string `json:"afterHash"`
	Mode      uint32 `json:"mode"`
}
type transaction struct {
	Changes []change `json:"changes"`
}
type installer struct {
	root, host  string
	localBinary string
	fetch       func(context.Context, string, int64) ([]byte, error)
	probe       func(string) error
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func suffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
func (i installer) binary() string    { return filepath.Join(i.root, "bin", "agentx-bridge-mcp"+suffix()) }
func (i installer) statePath() string { return filepath.Join(i.root, "installation.json") }
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentx_connect:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println("agentx-connect", buildVersion)
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: agentx-connect <install|upgrade|rollback|uninstall|status|doctor> --host <host> [--server <HTTPS origin>] [--version vX.Y.Z] [--no-connect] [--plugin]")
	}
	action := args[0]
	switch action {
	case "install", "upgrade", "rollback", "uninstall", "status", "doctor":
	default:
		return errors.New("unknown action")
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	host := f.String("host", "", "codex, claude, cursor, workbuddy, trae, lobi or openclaw")
	root := f.String("root", "", "absolute installation directory")
	server := f.String("server", "", "AgentX HTTPS origin")
	version := f.String("version", "", "explicit published version vX.Y.Z")
	noConnect := f.Bool("no-connect", false, "install files without requesting authorization")
	plugin := f.Bool("plugin", false, "use Git plugin's MCP and Skill; login only")
	noBrowser := f.Bool("no-open-browser", false, "print approval URL")
	localBinary := f.String("local-binary", "", "explicit locally compiled Bridge from the same public module version")
	if err := f.Parse(args[1:]); err != nil {
		return errors.New("invalid options; use --help")
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *localBinary != "" && (action != "install" && action != "upgrade") {
		return errors.New("--local-binary is only valid for install or upgrade")
	}
	if *localBinary != "" && !filepath.IsAbs(*localBinary) {
		return errors.New("--local-binary must be an absolute file path")
	}
	switch *host {
	case "codex", "claude", "cursor", "workbuddy", "trae", "lobi", "openclaw":
	default:
		return errors.New("select one supported Host")
	}
	if *plugin && *host != "codex" && *host != "claude" {
		return errors.New("Git plugin mode supports Codex or Claude")
	}
	if (action == "install" || action == "upgrade") && !versionPattern.MatchString(*version) {
		return errors.New("an explicit --version vX.Y.Z is required")
	}
	if (action == "install" || action == "upgrade") && buildVersion != "dev" && *version != "v"+buildVersion {
		return errors.New("installer release does not match --version; download the bootstrap from the selected release so binary and Skill versions agree")
	}
	if (action == "install" || action == "upgrade") && !*noConnect && !validOrigin(*server) {
		return errors.New("--server must be an HTTPS origin without credentials, path, query or fragment")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("resolve user home")
	}
	if *root == "" {
		*root = filepath.Join(home, ".agentx-connect", *host)
	}
	if !filepath.IsAbs(*root) || filepath.Clean(*root) == filepath.VolumeName(*root)+string(os.PathSeparator) {
		return errors.New("--root must be a non-root absolute directory")
	}
	i := installer{root: filepath.Clean(*root), host: *host, localBinary: *localBinary, fetch: fetchHTTPS, probe: probeBinary}
	if err := safePath(i.root); err != nil {
		return err
	}
	if err := os.MkdirAll(i.root, 0700); err != nil {
		return err
	}
	// OS advisory locks release on process death, unlike stale PID/lock-directory heuristics.
	unlock, err := lockRoot(i.root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := i.recover(); err != nil {
		return err
	}
	switch action {
	case "status":
		s, err := i.load()
		if err != nil {
			return err
		}
		if err := i.verify(s); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(s)
	case "doctor":
		s, err := i.load()
		if err != nil {
			return err
		}
		if err := i.verify(s); err != nil {
			return err
		}
		return runBridge(i.binary(), "doctor", *host)
	case "rollback":
		return i.rollback()
	case "uninstall":
		s, err := i.load()
		if errors.Is(err, os.ErrNotExist) {
			return i.uninstall()
		}
		if err != nil {
			return err
		}
		if err := i.verify(s); err != nil {
			return err
		}
		if !*plugin {
			if err := runBridge(i.binary(), "uninstall", *host); err != nil {
				return err
			}
		}
		return i.uninstall()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := i.install(ctx, *version, skillDestination(home, *host, *plugin), action == "upgrade"); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "agentx_connect: files_committed", *host, *version)
	if *noConnect {
		return nil
	}
	operation := "connect"
	if *plugin {
		operation = "login"
	}
	bridgeArgs := []string{operation, *host, "--server", *server}
	if *plugin {
		bridgeArgs = append(bridgeArgs, "--reuse")
	}
	if *noBrowser {
		bridgeArgs = append(bridgeArgs, "--no-open-browser")
	}
	// A failed/declined authorization leaves verified files installed, for doctor/retry.
	return runBridge(i.binary(), bridgeArgs...)
}
func validOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(value, "\r\n\t ")
}
func skillDestination(home, host string, plugin bool) string {
	if plugin {
		return ""
	}
	var dir string
	switch host {
	case "codex":
		dir = ".codex"
	case "claude":
		dir = ".claude"
	case "cursor":
		dir = ".cursor"
	case "trae":
		dir = ".trae"
	case "workbuddy":
		dir = ".workbuddy"
	case "lobi":
		return filepath.Join(home, ".config", "codewiz", "skills", "agentx-delivery-network", "SKILL.md")
	case "openclaw":
		dir = ".openclaw"
	}
	return filepath.Join(home, dir, "skills", "agentx-delivery-network", "SKILL.md")
}

// v0.2.0 used CodeBuddy CLI's directory for WorkBuddy Desktop. Only the exact
// old managed path is accepted for transactional migration, rollback and removal.
func managedSkillPath(home, host, path string) bool {
	return path == skillDestination(home, host, false) || (host == "workbuddy" && path == filepath.Join(home, ".codebuddy", "skills", "agentx-delivery-network", "SKILL.md"))
}
func runBridge(file string, args ...string) error {
	c := exec.Command(file, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return errors.New("Host step failed; installed files were retained. Run doctor or retry authorization")
	}
	return nil
}
func probeBinary(file string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, file, "version").Run() != nil {
		return errors.New("candidate binary cannot run; verify OS/CPU and system signing policy; source installation is documented in SOURCE_INSTALL.md")
	}
	return nil
}
func fetchHTTPS(ctx context.Context, address string, limit int64) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return nil, errors.New("download requires HTTPS")
	}
	client := http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil {
			return errors.New("unsafe redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("prepare download")
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("release download failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("release download HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("release download incomplete or oversized")
	}
	return b, nil
}
func checksum(body []byte, name string) (string, error) {
	var found string
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if found != "" {
				return "", errors.New("duplicate checksum")
			}
			found = fields[0]
		}
	}
	b, err := hex.DecodeString(found)
	if err != nil || len(b) != 32 {
		return "", errors.New("release checksum missing or invalid")
	}
	return strings.ToLower(found), nil
}
func (i installer) load() (state, error) {
	var s state
	if err := safePath(i.statePath()); err != nil {
		return s, err
	}
	b, err := os.ReadFile(i.statePath())
	if err != nil {
		return s, err
	}
	if len(b) > 8192 || json.Unmarshal(b, &s) != nil || s.Host != i.host || !versionPattern.MatchString(s.Version) || !validSource(s.Source) {
		return s, errors.New("invalid installation manifest")
	}
	home, err := os.UserHomeDir()
	if err != nil || (s.SkillPath != "" && !managedSkillPath(home, i.host, s.SkillPath)) {
		return s, errors.New("invalid managed Skill path")
	}
	return s, nil
}
func safePath(name string) error {
	for p := filepath.Clean(name); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("managed path must not contain symlinks")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func readManaged(path string) ([]byte, bool, error) {
	if err := safePath(path); err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}
func atomicWrite(path string, body []byte, mode os.FileMode) error {
	if err := safePath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agentx-stage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (i installer) install(ctx context.Context, version, skillPath string, upgrade bool) error {
	previous, err := i.load()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if previous.Version != version && !upgrade {
			return errors.New("different version installed; use upgrade")
		}
		if previous.SkillPath != skillPath && !(upgrade && i.host == "workbuddy" && previous.SkillPath != "" && skillPath != "") {
			return errors.New("installation mode changed; uninstall the previous mode first")
		}
		if err := i.verify(previous); err != nil {
			return err
		}
	}
	binary, exists, err := readManaged(i.binary())
	if err != nil {
		return err
	}
	if exists && (previous.BinaryHash == "" || digest(binary) != previous.BinaryHash) {
		return errors.New("installed binary was modified; refusing overwrite")
	}
	var oldSkill []byte
	if skillPath != "" {
		var present bool
		oldSkill, present, err = readManaged(skillPath)
		if err != nil {
			return err
		}
		if present && (previous.SkillPath != skillPath || digest(oldSkill) != previous.SkillHash) {
			return errors.New("Skill is user-owned or modified; refusing overwrite")
		}
	}
	if previous.SkillPath != "" && previous.SkillPath != skillPath {
		oldSkill, _, err = readManaged(previous.SkillPath)
		if err != nil {
			return err
		}
	}
	newBinary, source, err := i.candidate(ctx, version)
	if err != nil {
		return err
	}
	want := digest(newBinary)
	if previous.Version == version && previous.BinaryHash != want && !upgrade {
		return errors.New("same version has different bytes; use upgrade to preserve rollback")
	}
	stage := filepath.Join(i.root, "candidate"+suffix())
	if err := atomicWrite(stage, newBinary, 0700); err != nil {
		return err
	}
	defer os.Remove(stage)
	if err := i.probe(stage); err != nil {
		return err
	}
	next := state{Version: version, Host: i.host, BinaryHash: want, Source: source, SkillPath: skillPath}
	if skillPath != "" {
		next.SkillHash = digest(skill)
	}
	body, _ := json.MarshalIndent(next, "", "  ")
	updates := []update{{i.binary(), newBinary, 0700}, {i.statePath(), body, 0600}}
	if skillPath != "" {
		updates = append(updates, update{skillPath, skill, 0600})
	}
	if previous.SkillPath != "" && previous.SkillPath != skillPath {
		updates = append(updates, update{previous.SkillPath, nil, 0600})
	}
	if previous.Version != "" && (previous.Version != version || previous.BinaryHash != want || previous.SkillHash != next.SkillHash || previous.SkillPath != skillPath) {
		// Persist the previous verified release before changing active files.
		oldState, _ := json.Marshal(previous)
		for _, u := range []update{{filepath.Join(i.root, "previous-binary"), binary, 0600}, {filepath.Join(i.root, "previous-state.json"), oldState, 0600}, {filepath.Join(i.root, "previous-skill"), oldSkill, 0600}} {
			if err := atomicWrite(u.path, u.body, u.mode); err != nil {
				return err
			}
		}
	}
	return i.commit(updates)
}

type update struct {
	path string
	body []byte
	mode os.FileMode
}

func (i installer) commit(updates []update) error {
	var journal transaction
	for _, u := range updates {
		before, exists, err := readManaged(u.path)
		if err != nil {
			return err
		}
		after := digest(u.body)
		if u.body == nil {
			after = "ABSENT"
		}
		journal.Changes = append(journal.Changes, change{u.path, before, exists, after, uint32(u.mode)})
	}
	b, _ := json.Marshal(journal)
	if err := atomicWrite(filepath.Join(i.root, "transaction.json"), b, 0600); err != nil {
		return err
	}
	for _, u := range updates {
		var err error
		if u.body == nil {
			err = os.Remove(u.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			err = atomicWrite(u.path, u.body, u.mode)
		}
		if err != nil {
			if restoreErr := i.recover(); restoreErr != nil {
				return errors.New("installation interrupted; recovery requires closing the Host and rerunning this command")
			}
			return err
		}
	}
	// This deletion is the commit marker; a process killed earlier rolls back.
	return os.Remove(filepath.Join(i.root, "transaction.json"))
}
func (i installer) recover() error {
	path := filepath.Join(i.root, "transaction.json")
	body, exists, err := readManaged(path)
	if err != nil || !exists {
		return err
	}
	var journal transaction
	if len(body) > 100<<20 || json.Unmarshal(body, &journal) != nil || len(journal.Changes) > 4 {
		return errors.New("invalid recovery journal")
	}
	// Journal contents are local input, never permission to write arbitrary paths.
	for _, c := range journal.Changes {
		if c.Path != i.binary() && c.Path != i.statePath() {
			home, err := os.UserHomeDir()
			if err != nil || !managedSkillPath(home, i.host, c.Path) {
				return errors.New("unsafe recovery target")
			}
		}
		current, present, err := readManaged(c.Path)
		if err != nil {
			return err
		}
		if present && digest(current) != c.AfterHash && (!c.Existed || digest(current) != digest(c.Before)) {
			return errors.New("recovery target was modified; preserving user content")
		}
	}
	for _, c := range journal.Changes {
		if c.Existed {
			mode := os.FileMode(0600)
			if c.Path == i.binary() {
				mode = 0700
			}
			if err := atomicWrite(c.Path, c.Before, mode); err != nil {
				return err
			}
		} else {
			if err := os.Remove(c.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return os.Remove(path)
}
func (i installer) rollback() error {
	current, err := i.load()
	if err != nil {
		return err
	}
	if err := i.verify(current); err != nil {
		return err
	}
	var old state
	b, _, err := readManaged(filepath.Join(i.root, "previous-state.json"))
	if err != nil || json.Unmarshal(b, &old) != nil || old.Host != i.host || !versionPattern.MatchString(old.Version) || !validSource(old.Source) {
		return errors.New("no compatible previous release")
	}
	home, err := os.UserHomeDir()
	if err != nil || (old.SkillPath != "" && !managedSkillPath(home, i.host, old.SkillPath)) || (old.SkillPath == "") != (current.SkillPath == "") {
		return errors.New("no compatible previous Skill path")
	}
	if old.SkillPath != current.SkillPath {
		_, exists, err := readManaged(old.SkillPath)
		if err != nil || exists {
			return errors.New("previous Skill path is occupied; preserving user content")
		}
	}
	binary, _, err := readManaged(filepath.Join(i.root, "previous-binary"))
	if err != nil || digest(binary) != old.BinaryHash {
		return errors.New("previous binary checksum mismatch")
	}
	updates := []update{{i.binary(), binary, 0700}, {i.statePath(), b, 0600}}
	if old.SkillPath != "" {
		b, _, err := readManaged(filepath.Join(i.root, "previous-skill"))
		if err != nil || digest(b) != old.SkillHash {
			return errors.New("previous Skill checksum mismatch")
		}
		updates = append(updates, update{old.SkillPath, b, 0600})
	}
	if current.SkillPath != old.SkillPath {
		updates = append(updates, update{current.SkillPath, nil, 0600})
	}
	return i.commit(updates)
}
func (i installer) verify(s state) error {
	for path, want := range map[string]string{i.binary(): s.BinaryHash, s.SkillPath: s.SkillHash} {
		if path == "" {
			continue
		}
		b, present, err := readManaged(path)
		if err != nil {
			return err
		}
		if !present || digest(b) != want {
			return errors.New("managed file changed; preserving it")
		}
	}
	return nil
}
func (i installer) uninstall() error {
	s, err := i.load()
	if errors.Is(err, os.ErrNotExist) {
		_, exists, readErr := readManaged(i.binary())
		if readErr != nil {
			return readErr
		}
		if exists {
			return errors.New("binary has no installation manifest; preserving user content")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := i.verify(s); err != nil {
		return err
	}
	// Remove only exact managed files; credentials and user directories remain.
	var removals []update
	for _, path := range []string{s.SkillPath, i.binary(), i.statePath()} {
		if path != "" {
			removals = append(removals, update{path, nil, 0600})
		}
	}
	if err := i.commit(removals); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "agentx_connect: files_removed; revoke this Host separately in AgentX settings")
	return nil
}
