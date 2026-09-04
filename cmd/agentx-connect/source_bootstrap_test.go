package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSourceBootstrapBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX bootstrap")
	}
	for _, scenario := range []string{"install", "upgrade", "status", "compiler-failure", "installer-failure", "floating-version", "override-version", "override-binary"} {
		t.Run(scenario, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("SOURCE_TEST_DIR", dir)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SOURCE_TEST_FAIL", "0")
			t.Setenv("SOURCE_TEST_EXIT", "0")
			// Hostile inherited build configuration must not alter the chosen version.
			t.Setenv("GOFLAGS", "-toolexec=untrusted")
			t.Setenv("GOSUMDB", "off")
			t.Setenv("CGO_ENABLED", "1")
			stub := `#!/bin/sh
set -eu
[ "$GOFLAGS" = "" ] && [ "$GOSUMDB" = sum.golang.org ] && [ "$CGO_ENABLED" = 0 ] && [ "$GOTOOLCHAIN" = local ] && [ "$GOWORK" = off ]
printf '%s\n' "$@" > "$SOURCE_TEST_DIR/go-args"
printf '%s' "$GOBIN" > "$SOURCE_TEST_DIR/stage"
[ "$SOURCE_TEST_FAIL" = 0 ] || exit 17
cat > "$GOBIN/agentx-connect" <<'INSTALLER'
#!/bin/sh
printf '%s\n' "$@" > "$SOURCE_TEST_DIR/installer-args"
exit "$SOURCE_TEST_EXIT"
INSTALLER
chmod 0700 "$GOBIN/agentx-connect"
`
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			action, version := "install", "v1.2.3"
			extra := []string{"--host", "codex", "--root", filepath.Join(dir, "root with spaces"), "--no-connect"}
			switch scenario {
			case "upgrade", "status":
				action = scenario
			case "compiler-failure":
				t.Setenv("SOURCE_TEST_FAIL", "1")
			case "installer-failure":
				t.Setenv("SOURCE_TEST_EXIT", "73")
			case "floating-version":
				version = "latest"
			case "override-version":
				extra = append(extra, "--version=v9.9.9")
			case "override-binary":
				extra = append(extra, "--local-binary", "/arbitrary")
			}
			args := append([]string{"../../install-source.sh", version, action}, extra...)
			out, err := exec.Command("sh", args...).CombinedOutput()
			ok := scenario == "install" || scenario == "upgrade" || scenario == "status"
			if (err == nil) != ok {
				t.Fatalf("unexpected result: %v %s", err, out)
			}
			if scenario == "installer-failure" {
				if e, yes := err.(*exec.ExitError); !yes || e.ExitCode() != 73 {
					t.Fatal("lost installer exit code")
				}
			}
			goArgs, _ := os.ReadFile(filepath.Join(dir, "go-args"))
			installerArgs, _ := os.ReadFile(filepath.Join(dir, "installer-args"))
			rejected := scenario == "floating-version" || strings.HasPrefix(scenario, "override-")
			if rejected && len(goArgs) != 0 {
				t.Fatal("invalid parameters reached compiler")
			}
			if scenario == "compiler-failure" && len(installerArgs) != 0 {
				t.Fatal("partial build reached installer")
			}
			if !rejected {
				if !strings.Contains(string(goArgs), "github.com/"+repository+"/cmd/agentx-connect@v1.2.3") {
					t.Fatal("module version not pinned")
				}
				if strings.Contains(string(goArgs), "agentx-bridge-mcp@") != (action != "status") {
					t.Fatal("unexpected bridge build")
				}
				stage, err := os.ReadFile(filepath.Join(dir, "stage"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(string(stage)); !os.IsNotExist(err) {
					t.Fatal("build directory not cleaned")
				}
			}
			if ok && !strings.Contains(string(installerArgs), "\n"+filepath.Join(dir, "root with spaces")+"\n") {
				t.Fatal("arguments were split")
			}
		})
	}
}
