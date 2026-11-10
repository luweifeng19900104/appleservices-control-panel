package reconnectvault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testVault(t *testing.T) *Vault {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	key := bytes.Repeat([]byte{0x42}, 32)
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	vault, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return vault
}

func TestVaultRoundTripAndAccountBinding(t *testing.T) {
	vault := testVault(t)
	dir := t.TempDir()
	want := Credentials{Password: "sample-password", DeviceCodeURL: "https://codes.example.test/get?id=42", TwoFactor: "trusted-device"}
	if err := vault.Save(dir, "Owner@Example.test", want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(want.Password)) || bytes.Contains(raw, []byte(want.DeviceCodeURL)) {
		t.Fatal("credential plaintext was written to disk")
	}
	got, err := vault.Load(dir, "owner@example.test")
	if err != nil || got != want {
		t.Fatalf("round trip = %#v, %v", got, err)
	}
	if _, err := vault.Load(dir, "other@example.test"); err == nil {
		t.Fatal("encrypted credentials crossed account binding")
	}
	updated := Credentials{Password: "new-password", TwoFactor: "sms"}
	if err := vault.Save(dir, "owner@example.test", updated); err != nil {
		t.Fatal(err)
	}
	if got, err := vault.Load(dir, "owner@example.test"); err != nil || got != updated {
		t.Fatalf("updated round trip = %#v, %v", got, err)
	}
	if err := vault.Delete(dir); err != nil || vault.Available(dir, "owner@example.test") {
		t.Fatal("credential deletion failed")
	}
}

func TestLoadKeyRejectsWrongLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(path); err == nil {
		t.Fatal("short credential key was accepted")
	}
}
