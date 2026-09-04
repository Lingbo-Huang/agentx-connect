package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Compile the actual exported commands using Go's versioned module installer.
// A local module proxy replaces only this unpublished source snapshot; no Host
// registration, authorization, public release or platform access is performed.
func TestSourceBuildInstallUpgradeRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles versioned commands")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const version = "v0.0.0-source-test"
	module := "github.com/" + repository
	proxy := filepath.Join(dir, "proxy")
	versions := filepath.Join(proxy, "github.com", "!lingbo-!huang", "agentx-connect", "@v")
	if err := os.MkdirAll(versions, 0700); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"list": []byte(version + "\n"), version + ".mod": mod, version + ".info": []byte(`{"Version":"` + version + `","Time":"2026-09-04T00:00:00Z"}`)} {
		if err := os.WriteFile(filepath.Join(versions, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(versions, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	err = filepath.WalkDir(repo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(rel, ".go") || entry.Name() == "SKILL.md" || rel == "go.mod" || rel == "go.sum") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := z.Create(module + "@" + version + "/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	proxyPath := filepath.ToSlash(proxy)
	if !strings.HasPrefix(proxyPath, "/") {
		proxyPath = "/" + proxyPath
	}
	t.Setenv("GOPROXY", (&url.URL{Scheme: "file", Path: proxyPath}).String())
	t.Setenv("GOSUMDB", "off") // This version exists only in this test's local proxy.
	// Isolate the synthetic module to avoid reusing an earlier test's version.
	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	// Go supports comma-separated proxies; already downloaded dependency zips
	// remain available from the real module cache without network access.
	cachePath := filepath.ToSlash(filepath.Join(strings.TrimSpace(string(cache)), "cache", "download"))
	if !strings.HasPrefix(cachePath, "/") {
		cachePath = "/" + cachePath
	}
	t.Setenv("GOPROXY", os.Getenv("GOPROXY")+","+(&url.URL{Scheme: "file", Path: cachePath}).String())
	t.Setenv("GOMODCACHE", filepath.Join(dir, "modules"))
	t.Setenv("GOBIN", filepath.Join(dir, "built"))
	t.Setenv("CGO_ENABLED", "0")
	t.Setenv("GOFLAGS", "-modcacherw") // Keep this isolated synthetic cache removable by TempDir cleanup.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := func(file string, args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, file, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", filepath.Base(file), err, out)
		}
		return out
	}
	command("go", "install", "-trimpath", "-ldflags", "-X main.buildVersion="+strings.TrimPrefix(version, "v"), module+"/cmd/agentx-connect@"+version, module+"/cmd/agentx-bridge-mcp@"+version)
	local := filepath.Join(dir, "built", "agentx-bridge-mcp"+suffix())
	tool := filepath.Join(dir, "built", "agentx-connect"+suffix())
	root := filepath.Join(dir, "installed")
	command(tool, "install", "--host", "codex", "--root", root, "--version", version, "--local-binary", local, "--plugin", "--no-connect")
	var original state
	if err := json.Unmarshal(command(tool, "status", "--host", "codex", "--root", root), &original); err != nil || original.Source != "LOCAL_BUILD" {
		t.Fatalf("source manifest: %v %+v", err, original)
	}
	command("go", "install", "-trimpath", "-ldflags", "-X main.buildVersion="+strings.TrimPrefix(version, "v")+" -X main.buildCommit=rebuilt", module+"/cmd/agentx-bridge-mcp@"+version)
	command(tool, "upgrade", "--host", "codex", "--root", root, "--version", version, "--local-binary", local, "--plugin", "--no-connect")
	var rebuilt state
	if err := json.Unmarshal(command(tool, "status", "--host", "codex", "--root", root), &rebuilt); err != nil || rebuilt.BinaryHash == original.BinaryHash {
		t.Fatal("rebuild did not update actual bytes")
	}
	command(tool, "rollback", "--host", "codex", "--root", root)
	var restored state
	if err := json.Unmarshal(command(tool, "status", "--host", "codex", "--root", root), &restored); err != nil || restored != original {
		t.Fatal("rollback failed to restore source/hash")
	}
	command(tool, "uninstall", "--host", "codex", "--root", root, "--plugin")
}
