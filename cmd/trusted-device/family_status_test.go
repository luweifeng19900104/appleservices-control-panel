package main

import (
	"strings"
	"testing"
)

func TestParseFamilyStatusUsesOnlyMarkedSafeJSON(t *testing.T) {
	output := "diagnostic line\n" + familyStatusPrefix + `{"member":true,"members":3,"organizers":1,"self":1,"storage_known":true,"storage_participating":true,"storage_available":true,"storage_enabled":true,"storage_bytes":53687091200}` + "\n"
	status, err := parseFamilyStatus(output)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Member || status.Members != 3 || status.Organizers != 1 || status.Self != 1 || !status.StorageParticipating || status.StorageBytes != 53687091200 {
		t.Fatalf("unexpected family status: %#v", status)
	}
}

func TestParseFamilyStatusRejectsUnmarkedOutput(t *testing.T) {
	if _, err := parseFamilyStatus(`{"member":true}`); err == nil {
		t.Fatal("unmarked helper output accepted")
	}
}

func TestFamilyActionFailureExplainsReconnectRequirements(t *testing.T) {
	message, stage := familyActionFailure("pool member target check returned HTTP 401")
	if stage != "family-organizer-reconnect-required" || !strings.Contains(message, "重新连接") {
		t.Fatalf("organizer failure = %q %q", stage, message)
	}
	message, stage = familyActionFailure("target password environment is empty")
	if stage != "family-target-reconnect-required" || !strings.Contains(message, "当前子号") {
		t.Fatalf("target failure = %q %q", stage, message)
	}
}
