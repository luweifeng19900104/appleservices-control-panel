package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
)

type fakePurchaseReadback struct {
	subscription *icloud.SkillSubscriptionInfo
	storage      *appleservices.StorageInfo
}

func (f fakePurchaseReadback) ReadSkillStorageSubscription(context.Context) (*icloud.SkillSubscriptionInfo, error) {
	return f.subscription, nil
}

func (f fakePurchaseReadback) StorageInfo(context.Context) (*appleservices.StorageInfo, error) {
	return f.storage, nil
}

func TestPurchaseJournalIsPrivateAndRedacted(t *testing.T) {
	dir := t.TempDir()
	session := &appleservices.Session{
		AppleID: "owner@example.test",
		DSID:    "123456789",
		Tokens:  map[string]string{"mmeAuthToken": "secret-token"},
	}
	if err := writeJSONFile(filepath.Join(dir, "session.json"), session); err != nil {
		t.Fatal(err)
	}
	target := appleservices.PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	journal, err := startPurchaseAttemptJournal(dir, "storage-skill-purchase-us", target)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.markSubmitting(); err != nil {
		t.Fatal(err)
	}
	readback := fakePurchaseReadback{
		subscription: &icloud.SkillSubscriptionInfo{
			SubscriptionID: "private-subscription-id", Present: true, StorageAmount: "50 GB",
			PaidPrice: "$0.99", AutoRenew: true, Verified50GB: true,
		},
		storage: &appleservices.StorageInfo{TotalBytes: target.Bytes, PaidBytes: target.Bytes},
	}
	if err := journal.finish(readback, &appleservices.PurchaseResult{State: "completed", HTTPStatus: 200, AppleConfirmed: true, QuotaVerified: true}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(journal.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"owner@example.test", "123456789", "secret-token", "private-subscription-id"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("journal leaked private value %q", secret)
		}
	}
	var record purchaseAttemptRecord
	if json.Unmarshal(data, &record) != nil || !record.Verified || record.Stage != "verified" || !record.SubscriptionChecked || !record.QuotaChecked {
		t.Fatalf("journal did not preserve the safe verification result: %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(journal.path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("journal permissions are not owner-only: %v %v", info, err)
		}
	}
}
