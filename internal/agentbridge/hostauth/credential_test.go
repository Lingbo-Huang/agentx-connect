package hostauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureLocalCredentialPersistsPrivateAuthenticatedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "host.json")
	first, err := EnsureLocalCredential(path, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("EnsureLocalCredential: %v", err)
	}
	second, err := EnsureLocalCredential(path, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("second EnsureLocalCredential: %v", err)
	}
	if first != second {
		t.Fatalf("credential changed across restart: first=%#v second=%#v", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode=%o", info.Mode().Perm())
	}
	auth, err := NewStaticAuthenticator(first)
	if err != nil {
		t.Fatalf("NewStaticAuthenticator: %v", err)
	}
	caller, err := auth.AuthenticateHost(context.Background(), first.Credential)
	if err != nil || caller != first.Caller {
		t.Fatalf("caller=%#v err=%v", caller, err)
	}
	if _, err := auth.AuthenticateHost(context.Background(), "wrong"); err == nil {
		t.Fatal("AuthenticateHost accepted wrong credential")
	}
	updated, err := EnsureLocalCredential(path, "http://127.0.0.1:9999")
	if err != nil {
		t.Fatalf("update local endpoint: %v", err)
	}
	if updated.ServerURL != "http://127.0.0.1:9999" || updated.Credential != first.Credential || updated.Caller != first.Caller {
		t.Fatalf("endpoint update changed Host identity: %#v", updated)
	}
	reloaded, err := LoadCredentialBundle(path)
	if err != nil || reloaded != updated {
		t.Fatalf("reloaded=%#v err=%v", reloaded, err)
	}
}

func TestLoadCredentialBundleRejectsUnknownFieldsAndSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "host.json")
	body := `{"serverUrl":"https://agentx.example","credential":"secret","unknown":true,"caller":{"principalId":"principal-1","spaceId":"space-1","hostInstallationId":"host-1","hostKind":"CODEX"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentialBundle(path); err == nil {
		t.Fatal("LoadCredentialBundle accepted unknown fields")
	}
	link := filepath.Join(directory, "host-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentialBundle(link); err == nil {
		t.Fatal("LoadCredentialBundle accepted a symlink")
	}
}
