package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

func TestSourceMetadataCompatibility(t *testing.T) {
	for _, change := range []string{"none", "missing", "package", "module", "version", "replace", "os", "arch", "cgo"} {
		t.Run(change, func(t *testing.T) {
			module := "github.com/" + repository
			info := &debug.BuildInfo{Path: module + "/cmd/agentx-bridge-mcp", Main: debug.Module{Path: module, Version: "v1.0.0"}, Settings: []debug.BuildSetting{{Key: "GOOS", Value: runtime.GOOS}, {Key: "GOARCH", Value: runtime.GOARCH}, {Key: "CGO_ENABLED", Value: "0"}}}
			switch change {
			case "missing":
				info = nil
			case "package":
				info.Path = module + "/cmd/agentx-connect"
			case "module":
				info.Main.Path = "example.com/other"
			case "version":
				info.Main.Version = "v1.0.1"
			case "replace":
				info.Main.Replace = &debug.Module{Path: "example.com/other"}
			case "os":
				info.Settings[0].Value = "other"
			case "arch":
				info.Settings[1].Value = "other"
			case "cgo":
				info.Settings[2].Value = "1"
			}
			if compatibleBuild(info, "v1.0.0") != (change == "none") {
				t.Fatal("unexpected build compatibility")
			}
		})
	}
}

func TestLocalBinaryInputBoundary(t *testing.T) {
	i, _ := testInstaller(t)
	for _, kind := range []string{"relative", "directory", "empty", "oversize", "not-go", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			file := filepath.Join(i.root, kind)
			switch kind {
			case "relative":
				file = "relative"
			case "directory":
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(i.root, "not-go"), file); err != nil {
					t.Skip("symlinks unavailable")
				}
			default:
				f, err := os.Create(file)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "oversize" {
					err = f.Truncate(maxBinarySize + 1)
				} else if kind == "not-go" {
					_, err = f.WriteString("not an executable")
				}
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			i.localBinary = file
			i.fetch = func(context.Context, string, int64) ([]byte, error) {
				t.Fatal("local import contacted Release")
				return nil, nil
			}
			if _, _, err := i.candidate(context.Background(), "v1.0.0"); err == nil {
				t.Fatal("invalid source input accepted")
			}
		})
	}
}

func TestSameVersionRebuildRequiresUpgradeAndPreservesRollback(t *testing.T) {
	i, binary := testInstaller(t)
	ctx := context.Background()
	if err := i.install(ctx, "v1.0.0", "", false); err != nil {
		t.Fatal(err)
	}
	before, err := i.load()
	if err != nil {
		t.Fatal(err)
	}
	if before.Source != "GITHUB_RELEASE" {
		t.Fatal("missing release source")
	}
	*binary = []byte("same version, rebuilt or signed differently")
	if err := i.install(ctx, "v1.0.0", "", false); err == nil {
		t.Fatal("implicit byte replacement")
	}
	if err := i.verify(before); err != nil {
		t.Fatal("failed install changed original", err)
	}
	if err := i.install(ctx, "v1.0.0", "", true); err != nil {
		t.Fatal(err)
	}
	if err := i.rollback(); err != nil {
		t.Fatal(err)
	}
	if err := i.verify(before); err != nil {
		t.Fatal("rollback lost original", err)
	}
}
