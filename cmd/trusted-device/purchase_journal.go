package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/icloud"
	"github.com/Laky-64/appleservices/internal/control"
)

type purchaseAttemptRecord struct {
	Version                  int                       `json:"version"`
	AccountBindingHash       string                    `json:"account_binding_hash"`
	Action                   string                    `json:"action"`
	Target                   purchaseAttemptTarget     `json:"target"`
	StartedAt                time.Time                 `json:"started_at"`
	UpdatedAt                time.Time                 `json:"updated_at"`
	Stage                    string                    `json:"stage"`
	HTTPStatus               int                       `json:"http_status,omitempty"`
	AppleCode                string                    `json:"apple_code,omitempty"`
	PurchaseState            string                    `json:"purchase_state,omitempty"`
	ApplePurchaseConfirmed   bool                      `json:"apple_purchase_confirmed"`
	SubscriptionChecked      bool                      `json:"subscription_checked"`
	Subscription             *control.SubscriptionInfo `json:"subscription,omitempty"`
	SubscriptionFailureStage string                    `json:"subscription_failure_stage,omitempty"`
	QuotaChecked             bool                      `json:"quota_checked"`
	TotalBytes               uint64                    `json:"total_bytes,omitempty"`
	PaidBytes                uint64                    `json:"paid_bytes,omitempty"`
	QuotaFailureStage        string                    `json:"quota_failure_stage,omitempty"`
	Verified                 bool                      `json:"verified"`
}

type purchaseAttemptTarget struct {
	Bytes      uint64 `json:"bytes"`
	PriceMinor int64  `json:"price_minor"`
	Currency   string `json:"currency"`
	Period     string `json:"period"`
}

type purchaseAttemptJournal struct {
	path   string
	record purchaseAttemptRecord
}

func (j *purchaseAttemptJournal) save() error {
	if j == nil {
		return errors.New("purchase journal unavailable")
	}
	j.record.UpdatedAt = time.Now().UTC()
	if err := writeJSONFile(j.path, &j.record); err != nil {
		return errors.New("purchase journal update failed")
	}
	return nil
}

func (j *purchaseAttemptJournal) markSubmitting() error {
	j.record.Stage = "submitting"
	return j.save()
}

func startPurchaseAttemptJournal(dir, action string, target appleservices.PurchaseTarget) (*purchaseAttemptJournal, error) {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, errors.New("purchase journal account binding unavailable")
	}
	var session appleservices.Session
	if json.Unmarshal(data, &session) != nil || session.AppleID == "" || session.DSID == "" {
		return nil, errors.New("purchase journal account binding unavailable")
	}
	binding := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(session.AppleID)) + "\x00" + session.DSID))
	attemptDir := filepath.Join(dir, "purchase-attempts")
	if err := os.MkdirAll(attemptDir, 0700); err != nil {
		return nil, errors.New("purchase journal directory unavailable")
	}
	if err := os.Chmod(attemptDir, 0700); err != nil {
		return nil, errors.New("purchase journal directory permissions unavailable")
	}
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("purchase journal identifier unavailable")
	}
	now := time.Now().UTC()
	record := purchaseAttemptRecord{
		Version:            1,
		AccountBindingHash: hex.EncodeToString(binding[:]),
		Action:             action,
		Target: purchaseAttemptTarget{
			Bytes: target.Bytes, PriceMinor: target.PriceMinor, Currency: target.Currency, Period: target.Period,
		},
		StartedAt: now,
		UpdatedAt: now,
		Stage:     "started",
	}
	path := filepath.Join(attemptDir, now.Format("20060102T150405.000000000Z")+"-"+hex.EncodeToString(nonce)+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("purchase journal creation failed")
	}
	encodeErr := json.NewEncoder(file).Encode(record)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil || syncErr != nil || closeErr != nil {
		os.Remove(path)
		return nil, errors.New("purchase journal creation failed")
	}
	return &purchaseAttemptJournal{path: path, record: record}, nil
}

func safeSubscriptionInfo(info *icloud.SkillSubscriptionInfo) *control.SubscriptionInfo {
	if info == nil {
		return nil
	}
	return &control.SubscriptionInfo{
		Present: info.Present, StorageAmount: info.StorageAmount, PaidPrice: info.PaidPrice,
		AutoRenew: info.AutoRenew, Verified50GB: info.Verified50GB,
	}
}

func safeStorageFailureStage(err error) string {
	if err == nil {
		return ""
	}
	var failure *icloud.StorageError
	if errors.As(err, &failure) {
		return failure.Stage
	}
	return "readback"
}

func (j *purchaseAttemptJournal) finish(active any, purchase *appleservices.PurchaseResult, purchaseErr error) error {
	if j == nil {
		return errors.New("purchase journal unavailable")
	}
	j.record.Stage = "returned"
	if purchase != nil {
		j.record.PurchaseState = purchase.State
		j.record.HTTPStatus = purchase.HTTPStatus
		j.record.ApplePurchaseConfirmed = purchase.AppleConfirmed
	}
	var failure *icloud.StorageError
	if errors.As(purchaseErr, &failure) {
		j.record.Stage = failure.Stage
		j.record.HTTPStatus = failure.HTTPStatus
		j.record.AppleCode = failure.AppleCode
	} else if purchaseErr != nil {
		j.record.Stage = "purchase-error"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if reader, ok := active.(interface {
		ReadSkillStorageSubscription(context.Context) (*icloud.SkillSubscriptionInfo, error)
	}); ok {
		subscription, err := reader.ReadSkillStorageSubscription(ctx)
		if err == nil {
			j.record.SubscriptionChecked = true
			j.record.Subscription = safeSubscriptionInfo(subscription)
		} else {
			j.record.SubscriptionFailureStage = safeStorageFailureStage(err)
		}
	}
	if reader, ok := active.(interface {
		StorageInfo(context.Context) (*appleservices.StorageInfo, error)
	}); ok {
		quota, err := reader.StorageInfo(ctx)
		if err == nil && quota != nil {
			j.record.QuotaChecked = true
			j.record.TotalBytes = quota.TotalBytes
			j.record.PaidBytes = quota.PaidBytes
		} else {
			j.record.QuotaFailureStage = safeStorageFailureStage(err)
		}
	}
	j.record.Verified = j.record.Subscription != nil && j.record.Subscription.Verified50GB &&
		j.record.QuotaChecked && j.record.TotalBytes >= j.record.Target.Bytes && j.record.PaidBytes >= j.record.Target.Bytes
	if j.record.Verified {
		j.record.Stage = "verified"
	}
	return j.save()
}
