package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appleservices "github.com/Laky-64/appleservices"
)

func TestParseActivationPlanReturnsOnlySafeTermsMetadata(t *testing.T) {
	output := []byte("diagnostic\n" + `{"terms-plan":true,"region":"US","icloud-version":"630101","media-version":"290571"}` + "\n")
	plan, err := parseActivationPlan(output)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Required || activationTuple(plan) != "US:630101:290571" || plan.ICloudTermsURL == "" || plan.MediaTermsURL == "" {
		t.Fatalf("unexpected activation plan: %#v", plan)
	}
}

func TestActivationPlanCommandErrorExplainsExpiredCheckpoint(t *testing.T) {
	output := []byte(`{"gsa-authenticated":true,"HTTP":401,"used-gsa-checkpoint":true}` + "\naccount setup page was refused\n")
	if got := activationPlanCommandError(output).Error(); got != "Apple 激活会话已过期，请使用当前密码重新连接该账号后再检查" {
		t.Fatalf("expired checkpoint message = %q", got)
	}
	if got := activationPlanCommandError([]byte("transport failed")).Error(); got != "无法读取 Apple 当前条款状态" {
		t.Fatalf("generic activation message = %q", got)
	}
}

func TestActivationConsentAndRefreshedCheckpointPersistSafely(t *testing.T) {
	dir := t.TempDir()
	tuple := "US:630101:"
	if err := saveActivationConsent(dir, tuple); err != nil || !activationConsentReady(dir, tuple) || activationConsentReady(dir, "US:other:") {
		t.Fatalf("activation consent was not bound to its versions: %v", err)
	}
	current := appleservices.Session{AppleID: "owner@example.test", DSID: "123", Tokens: map[string]string{"com.apple.gs.idms.pet": "old"}}
	fresh := appleservices.Session{AppleID: current.AppleID, DSID: current.DSID, Tokens: map[string]string{
		"com.apple.gs.idms.pet": "fresh", "com.apple.gs.icloud.family.auth": "family",
	}, Cookies: []appleservices.Cookie{{URL: "https://idmsa.apple.com", Name: "session", Value: "private"}}}
	currentData, _ := json.Marshal(current)
	freshData, _ := json.Marshal(fresh)
	destination, source := filepath.Join(dir, "session.json"), filepath.Join(dir, "fresh.json")
	if os.WriteFile(destination, currentData, 0600) != nil || os.WriteFile(source, freshData, 0600) != nil {
		t.Fatal("could not prepare checkpoint test")
	}
	if err := installRefreshedGSACheckpoint(source, destination, current.AppleID); err != nil {
		t.Fatal(err)
	}
	var installed appleservices.Session
	data, _ := os.ReadFile(destination)
	if json.Unmarshal(data, &installed) != nil || installed.Tokens["com.apple.gs.idms.pet"] != "fresh" {
		t.Fatal("fresh checkpoint was not installed")
	}
	if _, err := os.Stat(destination + ".before-activation-refresh"); err != nil {
		t.Fatal("old checkpoint backup was not preserved")
	}
	fresh.DSID = "other"
	freshData, _ = json.Marshal(fresh)
	if os.WriteFile(source, freshData, 0600) != nil {
		t.Fatal("could not update mismatched checkpoint")
	}
	if err := installRefreshedGSACheckpoint(source, destination, current.AppleID); err == nil {
		t.Fatal("cross-account checkpoint was accepted")
	}
}

func TestActivationWorkspaceUsesProcessTempDirectory(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)
	runDir, err := createActivationWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runDir)
	if filepath.Dir(runDir) != tempRoot {
		t.Fatalf("activation workspace = %q, want parent %q", runDir, tempRoot)
	}
	info, err := os.Stat(runDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("activation workspace is unavailable: %v", err)
	}
}

func TestVerifiedGSACheckpointRequiresAccountBoundPET(t *testing.T) {
	dir := t.TempDir()
	session := appleservices.Session{AppleID: "owner@example.test", DSID: "123", Tokens: map[string]string{"com.apple.gs.idms.pet": "private"}}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if !verifiedGSACheckpointReady(dir, "OWNER@example.test") {
		t.Fatal("valid account-bound GSA checkpoint was rejected")
	}
	if verifiedGSACheckpointReady(dir, "other@example.test") {
		t.Fatal("GSA checkpoint crossed account binding")
	}
	session.Tokens = nil
	data, _ = json.Marshal(session)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if verifiedGSACheckpointReady(dir, session.AppleID) {
		t.Fatal("checkpoint without PET was accepted")
	}
}

func TestSnapshotFallsBackFromStaleSelectionAndShowsActivation(t *testing.T) {
	root := t.TempDir()
	accountDir := filepath.Join(root, "accounts", "01-account")
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		t.Fatal(err)
	}
	session := appleservices.Session{AppleID: "owner@example.test", DSID: "123", Tokens: map[string]string{"com.apple.gs.idms.pet": "private"}}
	data, _ := json.Marshal(session)
	if err := os.WriteFile(filepath.Join(accountDir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{Dir: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	server.accounts = []*Account{{ID: "01-account", Name: "owner", AppleID: session.AppleID, Dir: accountDir}}

	view := server.snapshotFor(context.Background(), "removed-account")
	if view.Selected != "01-account" || !view.ActivationReady || view.ICloudActivated {
		t.Fatalf("stale selection did not recover the activation workflow: %#v", view)
	}
}
