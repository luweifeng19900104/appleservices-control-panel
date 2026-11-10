package dashboard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/internal/reconnectvault"
)

func TestEncryptedReconnectCredentialsCanStartWithoutReentry(t *testing.T) {
	root := t.TempDir()
	keyPath := filepath.Join(root, "credential.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0x51}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Dir: root, Worker: filepath.Join(root, "missing-worker"), CredentialKeyFile: keyPath, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	account := &Account{ID: "01-account", AppleID: "owner@example.test", Dir: filepath.Join(root, "accounts", "01-account")}
	server.accounts = []*Account{account}
	want := credentials{AppleID: account.AppleID, Password: "saved-password", DeviceCodeURL: "https://codes.example.test/get?id=42", TwoFactor: "trusted-device"}
	if err := server.saveReconnectCredentials(account, want); err != nil {
		t.Fatal(err)
	}
	if !server.reconnectCredentialsAvailable(account) {
		t.Fatal("saved reconnect credentials were not reported")
	}
	raw, err := os.ReadFile(filepath.Join(account.Dir, reconnectvault.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(want.Password)) || bytes.Contains(raw, []byte(want.DeviceCodeURL)) {
		t.Fatal("dashboard wrote reconnect credentials in plaintext")
	}
	loaded, err := server.loadReconnectCredentials(account)
	if err != nil || loaded.Password != want.Password || loaded.DeviceCodeURL != want.DeviceCodeURL {
		t.Fatalf("loaded credentials = %#v, %v", loaded, err)
	}
	result := server.startAccount(context.Background(), account, credentials{})
	if strings.Contains(result.Message, "请输入") || result.Message != "缺少后台程序，请重新运行启动脚本" {
		t.Fatalf("saved reconnect was not used: %+v", result)
	}
	if err := server.forgetReconnectCredentials(account); err != nil || server.reconnectCredentialsAvailable(account) {
		t.Fatal("saved reconnect credentials were not deleted")
	}
}
