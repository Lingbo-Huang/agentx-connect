package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
)

const maxBinarySize = 64 << 20

func validSource(source string) bool {
	// Empty is the manifest format used through v0.2.2.
	return source == "" || source == "GITHUB_RELEASE" || source == "LOCAL_BUILD"
}

func (i installer) candidate(ctx context.Context, version string) ([]byte, string, error) {
	if i.localBinary != "" {
		body, err := readLocalBinary(i.localBinary)
		if err != nil {
			return nil, "", err
		}
		info, err := buildinfo.Read(bytes.NewReader(body))
		if err != nil || !compatibleBuild(info, version) {
			return nil, "", errors.New("local binary must be built from the exact public module version for this OS/CPU with CGO disabled")
		}
		// Build metadata checks compatibility, not publisher identity. The caller
		// explicitly trusts their source/compiler; never call this a signed release.
		return body, "LOCAL_BUILD", nil
	}
	asset := "agentx-bridge-mcp_" + runtime.GOOS + "_" + runtime.GOARCH + suffix()
	base := "https://github.com/" + repository + "/releases/download/" + version + "/"
	sums, err := i.fetch(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return nil, "", err
	}
	want, err := checksum(sums, asset)
	if err != nil {
		return nil, "", err
	}
	body, err := i.fetch(ctx, base+asset, maxBinarySize)
	if err != nil {
		return nil, "", err
	}
	if digest(body) != want {
		return nil, "", errors.New("binary checksum mismatch; existing installation preserved")
	}
	return body, "GITHUB_RELEASE", nil
}

func readLocalBinary(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("local binary path must be absolute")
	}
	if err := safePath(path); err != nil {
		return nil, err
	}
	stat, err := os.Lstat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > maxBinarySize {
		return nil, errors.New("local binary must be a nonempty regular file of at most 64 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read local binary")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(stat, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("local binary changed during read")
	}
	body, err := io.ReadAll(io.LimitReader(f, maxBinarySize+1))
	if err != nil || int64(len(body)) != stat.Size() || len(body) > maxBinarySize {
		return nil, errors.New("local binary incomplete, changed or oversized")
	}
	return body, nil
}

func compatibleBuild(info *debug.BuildInfo, version string) bool {
	module := "github.com/" + repository
	if info == nil || info.Path != module+"/cmd/agentx-bridge-mcp" || info.Main.Path != module || info.Main.Version != version || info.Main.Replace != nil {
		return false
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings["GOOS"] == runtime.GOOS && settings["GOARCH"] == runtime.GOARCH && settings["CGO_ENABLED"] == "0"
}
