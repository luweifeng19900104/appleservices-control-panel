package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	appleservices "github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/internal/platform"
)

const activationConsentFile = "activation-consent.json"

var errActivationCheckpointExpired = errors.New("Apple 激活会话已过期，请使用当前密码重新连接该账号后再检查")

func verifiedGSACheckpointReady(dir, appleID string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil || len(data) > 2<<20 {
		return false
	}
	var session appleservices.Session
	if json.Unmarshal(data, &session) != nil || !strings.EqualFold(session.AppleID, appleID) || session.DSID == "" {
		return false
	}
	return session.Tokens["com.apple.gs.idms.pet"] != ""
}

func activationTermsURLs(region string) (string, string) {
	icloudURL := "https://www.apple.com/legal/internet-services/icloud/"
	mediaURL := "https://www.apple.com/legal/internet-services/itunes/us/terms.html"
	if !strings.EqualFold(region, "US") {
		mediaURL = "https://www.apple.com/legal/internet-services/itunes/"
	}
	return icloudURL, mediaURL
}

func parseActivationPlan(output []byte) (*control.ActivationInfo, error) {
	for _, line := range strings.Split(string(output), "\n") {
		var value struct {
			TermsPlan     bool   `json:"terms-plan"`
			Region        string `json:"region"`
			ICloudVersion string `json:"icloud-version"`
			MediaVersion  string `json:"media-version"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &value) != nil || !value.TermsPlan {
			continue
		}
		if value.Region == "" || value.ICloudVersion == "" {
			return nil, errors.New("Apple 未返回完整条款版本")
		}
		icloudURL, mediaURL := activationTermsURLs(value.Region)
		return &control.ActivationInfo{Required: true, Region: value.Region, ICloudVersion: value.ICloudVersion, MediaVersion: value.MediaVersion, ICloudTermsURL: icloudURL, MediaTermsURL: mediaURL}, nil
	}
	return nil, errors.New("Apple 未返回可确认的 iCloud 条款计划")
}

func activationPlanCommandError(output []byte) error {
	for _, line := range strings.Split(string(output), "\n") {
		var facts struct {
			HTTP              int  `json:"HTTP"`
			GSAAuthenticated  bool `json:"gsa-authenticated"`
			UsedGSACheckpoint bool `json:"used-gsa-checkpoint"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &facts) != nil {
			continue
		}
		if facts.HTTP == 401 && facts.GSAAuthenticated && facts.UsedGSACheckpoint {
			return errActivationCheckpointExpired
		}
	}
	return errors.New("无法读取 Apple 当前条款状态")
}

func activationTuple(info *control.ActivationInfo) string {
	if info == nil {
		return ""
	}
	return info.Region + ":" + info.ICloudVersion + ":" + info.MediaVersion
}

func filteredActivationEnv(appleID, password string) []string {
	result := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "APPLESERVICES_APPLEID"), strings.EqualFold(key, "APPLESERVICES_PASSWORD"),
			strings.EqualFold(key, "HTTP_PROXY"), strings.EqualFold(key, "HTTPS_PROXY"), strings.EqualFold(key, "ALL_PROXY"),
			strings.EqualFold(key, "APPLESERVICES_SOCKS_PROXY"), strings.EqualFold(key, "APPLESERVICES_DELEGATE_DUMP"),
			strings.EqualFold(key, "APPLESERVICES_DELEGATE_PATH"):
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "APPLESERVICES_APPLEID="+appleID, "APPLESERVICES_DELEGATE_PATH=v2", "NO_PROXY=*")
	if password != "" {
		result = append(result, "APPLESERVICES_PASSWORD="+password)
	}
	return result
}

func runActivationCommand(ctx context.Context, root, appleID, password, executable string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	configureProcess(command)
	command.Dir = root
	command.Env = filteredActivationEnv(appleID, password)
	output, err := command.CombinedOutput()
	if len(output) > 256<<10 {
		return nil, errors.New("激活组件返回内容过大")
	}
	return output, err
}

func copyActivationTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || strings.HasPrefix(relative, "..") || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("激活运行时路径无效")
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		return errors.Join(copyErr, closeErr)
	})
}

func (s *Server) activationRoot() string {
	return filepath.Dir(filepath.Dir(s.config.MacPushSetup))
}

func (s *Server) activationPlan(ctx context.Context, account *Account, runDir string) (*control.ActivationInfo, error) {
	root := s.activationRoot()
	helper := filepath.Join(filepath.Dir(s.config.Worker), platform.Exe("accountsetupcheck"))
	dump := filepath.Join(runDir, "setup.html")
	output, err := runActivationCommand(ctx, root, account.AppleID, "", helper,
		"-dir", account.Dir, "-resume", filepath.Join(account.Dir, "session.json"), "-dump", dump)
	if err == nil {
		return parseActivationPlan(output)
	}
	if !errors.Is(activationPlanCommandError(output), errActivationCheckpointExpired) {
		return nil, activationPlanCommandError(output)
	}
	creds, loadErr := s.loadReconnectCredentials(account)
	if loadErr != nil {
		return nil, errActivationCheckpointExpired
	}
	checkpoint := filepath.Join(runDir, "fresh-session.json")
	freshDump := filepath.Join(runDir, "setup-fresh.html")
	output, err = runActivationCommand(ctx, root, account.AppleID, creds.Password, helper,
		"-dir", account.Dir, "-checkpoint", checkpoint, "-dump", freshDump)
	creds.Password = ""
	if err != nil {
		return nil, errors.New("已保存的密码需要新的验证码，请重新连接当前账号后再检查")
	}
	if err := installRefreshedGSACheckpoint(checkpoint, filepath.Join(account.Dir, "session.json"), account.AppleID); err != nil {
		return nil, errors.New("Apple 激活会话已刷新，但本地保存失败")
	}
	return parseActivationPlan(output)
}

func readTermsReceipt(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return false
	}
	var receipt struct {
		Success bool `json:"success"`
	}
	return json.Unmarshal(data, &receipt) == nil && receipt.Success
}

type activationConsentRecord struct {
	Tuple      string `json:"tuple"`
	AcceptedAt string `json:"accepted_at"`
}

func activationConsentReady(dir, tuple string) bool {
	data, err := os.ReadFile(filepath.Join(dir, activationConsentFile))
	if err != nil || len(data) > 4096 {
		return false
	}
	var record activationConsentRecord
	return json.Unmarshal(data, &record) == nil && record.Tuple == tuple && record.AcceptedAt != ""
}

func replacePrivateFile(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".activation-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func saveActivationConsent(dir, tuple string) error {
	data, err := json.Marshal(activationConsentRecord{Tuple: tuple, AcceptedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return replacePrivateFile(filepath.Join(dir, activationConsentFile), data)
}

func installRefreshedGSACheckpoint(source, destination, appleID string) error {
	data, err := os.ReadFile(source)
	if err != nil || len(data) == 0 || len(data) > 2<<20 {
		return errors.New("fresh GSA checkpoint is unavailable")
	}
	currentData, err := os.ReadFile(destination)
	if err != nil || len(currentData) == 0 || len(currentData) > 2<<20 {
		return errors.New("current GSA checkpoint is unavailable")
	}
	var current, fresh appleservices.Session
	if json.Unmarshal(currentData, &current) != nil || json.Unmarshal(data, &fresh) != nil ||
		!strings.EqualFold(current.AppleID, appleID) || !strings.EqualFold(fresh.AppleID, current.AppleID) ||
		current.DSID == "" || fresh.DSID != current.DSID || fresh.Tokens["com.apple.gs.idms.pet"] == "" ||
		fresh.Tokens["com.apple.gs.icloud.family.auth"] == "" || len(fresh.Cookies) == 0 {
		return errors.New("fresh GSA checkpoint account binding failed")
	}
	backup := destination + ".before-activation-refresh"
	if file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		_, writeErr := file.Write(currentData)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	return replacePrivateFile(destination, data)
}

func installAcceptedCheckpoint(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil || len(data) == 0 || len(data) > 2<<20 {
		return errors.New("Apple 验收会话不可用")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func createActivationWorkspace() (string, error) {
	// The release tree is immutable under the Linux systemd sandbox. An empty
	// parent makes MkdirTemp use TMPDIR, which the service pins to its private,
	// writable /data/appleservices/tmp directory.
	return os.MkdirTemp("", "dashboard-activation-")
}

func (s *Server) activationAction(parent context.Context, account *Account, accept bool, approvedTuple string) control.Result {
	if account == nil || !verifiedGSACheckpointReady(account.Dir, account.AppleID) {
		return control.Result{Message: "请先完成账号密码与验证码验证", Stage: "activation-session"}
	}
	if acceptedCheckpointReady(account.Dir, account.AppleID) {
		result := s.acceptedStorageAction(parent, account, false)
		result.Activation = &control.ActivationInfo{Completed: result.OK}
		return result
	}
	if !s.launchMu.TryLock() {
		return control.Result{Message: "当前账号正在处理其他登录或激活操作", Stage: "activation-busy"}
	}
	defer s.launchMu.Unlock()
	runDir, err := createActivationWorkspace()
	if err != nil {
		return control.Result{Message: "无法创建私有激活工作区", Stage: "activation-local"}
	}
	defer os.RemoveAll(runDir)
	root := s.activationRoot()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	plan, err := s.activationPlan(ctx, account, runDir)
	if err != nil {
		return control.Result{Message: err.Error(), Stage: "activation-plan"}
	}
	if !accept {
		return control.Result{OK: true, Message: "Apple 要求接受当前 iCloud 与媒体服务条款", Stage: "activation-terms", Activation: plan}
	}
	if approvedTuple == "" || approvedTuple != activationTuple(plan) {
		return control.Result{Message: "Apple 条款版本已变化，请重新查看并确认", Stage: "activation-consent", Activation: plan}
	}
	helper := filepath.Join(filepath.Dir(s.config.Worker), platform.Exe("accountsetupcheck"))
	if !activationConsentReady(account.Dir, approvedTuple) {
		termsDump := filepath.Join(runDir, "terms.html")
		_, _ = runActivationCommand(ctx, root, account.AppleID, "", helper,
			"-dir", account.Dir, "-resume", filepath.Join(account.Dir, "session.json"), "-accept-terms", "-expected-terms", approvedTuple, "-dump", termsDump)
		if !readTermsReceipt(termsDump + ".update.json") {
			return control.Result{Message: "Apple 未确认条款接受状态", Stage: "activation-terms", Activation: plan}
		}
		if err := saveActivationConsent(account.Dir, approvedTuple); err != nil {
			return control.Result{Message: "Apple 已确认条款，但本地状态未保存", Stage: "activation-save", Activation: plan}
		}
	}
	runtimeSource := filepath.Join(root, ".cache", "mac-nac-runtime")
	runtimeCopy := filepath.Join(runDir, "nac-runtime")
	if err := copyActivationTree(runtimeSource, runtimeCopy); err != nil {
		return control.Result{Message: "无法准备 Mac NAC 运行时", Stage: "activation-nac"}
	}
	validation := filepath.Join(runDir, "nac-validation.bin")
	nacOutput, nacErr := runActivationCommand(ctx, root, account.AppleID, "", s.config.Python,
		filepath.Join(root, "tools", "mac-nac-check.py"), "--profile", filepath.Join(account.Dir, "native-adi", "hw_info.plist"),
		"--runtime", runtimeCopy, "--encrypt-dll", filepath.Join(root, ".cache", "activation-source-review", "mac-io-encrypt.dll"),
		"--validation-out", validation, "--synthetic-missing-board-fields", "--transport", filepath.Join(filepath.Dir(s.config.Worker), platform.Exe("nacexchange")))
	if nacErr != nil || !strings.Contains(string(nacOutput), `"stage": "NACSign"`) {
		return control.Result{Message: "Mac NAC 生成未完成", Stage: "activation-nac"}
	}
	checkpoint := filepath.Join(runDir, "icloud-accepted.json")
	delegateOutput, delegateErr := runActivationCommand(ctx, root, account.AppleID, "", helper,
		"-dir", account.Dir, "-resume", filepath.Join(account.Dir, "session.json"), "-delegate-only", "-terms-accepted",
		"-nac-validation", validation, "-dump", filepath.Join(runDir, "delegate.plist"), "-icloud-checkpoint", checkpoint)
	if delegateErr != nil && (strings.Contains(string(delegateOutput), "HTTP 401") || strings.Contains(string(delegateOutput), "UNAUTHORIZED")) {
		if creds, loadErr := s.loadReconnectCredentials(account); loadErr == nil {
			freshSession := filepath.Join(runDir, "delegate-session.json")
			delegateOutput, delegateErr = runActivationCommand(ctx, root, account.AppleID, creds.Password, helper,
				"-dir", account.Dir, "-checkpoint", freshSession, "-delegate-only", "-terms-accepted",
				"-nac-validation", validation, "-dump", filepath.Join(runDir, "delegate-fresh.plist"), "-icloud-checkpoint", checkpoint)
			creds.Password = ""
			if delegateErr == nil {
				_ = installRefreshedGSACheckpoint(freshSession, filepath.Join(account.Dir, "session.json"), account.AppleID)
			}
		}
	}
	if delegateErr != nil || !strings.Contains(string(delegateOutput), "delegate accepted; mme-token-present=true") || !strings.Contains(string(delegateOutput), "quota accepted;") {
		return control.Result{Message: "Apple 尚未接受 Mac v2/NAC 激活", Stage: "activation-delegate"}
	}
	destination := filepath.Join(account.Dir, "icloud-accepted.json")
	if err := installAcceptedCheckpoint(checkpoint, destination); err != nil && !acceptedCheckpointReady(account.Dir, account.AppleID) {
		return control.Result{Message: "激活成功，但本地验收状态未保存", Stage: "activation-save"}
	}
	result := s.acceptedStorageAction(ctx, account, false)
	if !result.OK {
		return result
	}
	result.Stage = "activation-completed"
	result.Message = "iCloud 条款、Mac NAC、delegate 与配额验收均已完成"
	result.Activation = &control.ActivationInfo{Completed: true, Region: plan.Region, ICloudVersion: plan.ICloudVersion, MediaVersion: plan.MediaVersion, ICloudTermsURL: plan.ICloudTermsURL, MediaTermsURL: plan.MediaTermsURL}
	return result
}
