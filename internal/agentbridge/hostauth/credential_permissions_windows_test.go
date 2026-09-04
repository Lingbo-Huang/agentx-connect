package hostauth

import (
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/Lingbo-Huang/agentx-connect/pkg/agentbridge/v1"
	"golang.org/x/sys/windows"
)

func TestWindowsCredentialLifecycleAndACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	value := CredentialBundle{ServerURL: "https://agentx.example", Credential: "test-only-credential", Caller: protocol.CallerContext{
		PrincipalID: "principal-test", SpaceID: "space-test", HostInstallationID: "host-test", HostKind: protocol.HostKindCodex,
	}}
	if err := SaveCredentialBundle(path, value, false); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadCredentialBundle(path); err != nil || got != value {
		t.Fatalf("initial load failed: %v", err)
	}
	value.Credential = "test-only-replacement"
	if err := SaveCredentialBundle(path, value, true); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadCredentialBundle(path); err != nil || got != value {
		t.Fatalf("replacement failed: %v", err)
	}
	// A valid JSON credential with Everyone read access must fail closed.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentialBundle(path); err == nil {
		t.Fatal("accepted Everyone ACL")
	}
	if err := SaveCredentialBundle(path, value, true); err == nil {
		t.Fatal("replaced untrusted credential")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := secureCredentialFile(f); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentialBundle(path); err != nil {
		t.Fatal(err)
	}
}
