package appleservices

import (
	"context"
	"errors"
	"testing"

	"github.com/Laky-64/appleservices/icloud"
)

func TestStoreAuthenticationRequiresLivePassword(t *testing.T) {
	login := &Login{client: &Client{DSID: "123"}, creds: Credentials{AppleID: "owner@example.test"}}
	if login.StorePasswordReady() || login.StoreSessionReady() {
		t.Fatal("passwordless login reported Store readiness")
	}
	_, err := login.CheckSkillStoragePurchaseSession(context.Background(), nil)
	var failure *icloud.StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-password-required" {
		t.Fatalf("passwordless Store authentication was not blocked: %v", err)
	}
	login.creds.Password = "temporary-password"
	if !login.StorePasswordReady() || login.StoreSessionReady() {
		t.Fatal("live password and Store session readiness were conflated")
	}
}

func TestSkillPurchaseRequiresAuthenticatedStoreSession(t *testing.T) {
	login := &Login{client: &Client{DSID: "123"}}
	_, err := login.SubmitSkillStoragePurchase(context.Background(), PurchaseTarget{}, "")
	var failure *icloud.StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-session" {
		t.Fatalf("purchase without a Store session was not rejected safely: %v", err)
	}
}
