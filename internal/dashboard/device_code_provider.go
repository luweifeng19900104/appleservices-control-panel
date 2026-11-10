package dashboard

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/internal/control"
)

const (
	credentialImportSeparator = "----"
	deviceCodePollTimeout     = 3 * time.Minute
	deviceCodeResponseLimit   = 32 << 10
)

var (
	sixDigitCodePattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]{6})(?:[^0-9]|$)`)
	blockedProviderNets = []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
)

func normalizeCredentials(creds *credentials) error {
	if creds == nil {
		return errors.New("登录参数无效")
	}
	if creds.ImportLine != "" {
		line := strings.TrimSpace(creds.ImportLine)
		creds.ImportLine = ""
		parts := strings.Split(line, credentialImportSeparator)
		if len(parts) != 3 {
			return errors.New("导入格式应为：Apple 账户----密码----设备码接收链接")
		}
		creds.AppleID = strings.TrimSpace(parts[0])
		creds.Password = parts[1]
		creds.DeviceCodeURL = strings.TrimSpace(parts[2])
		creds.TwoFactor = "trusted-device"
	}
	if strings.TrimSpace(creds.AppleID) == "" || creds.Password == "" {
		return errors.New("请输入 Apple 账户和密码")
	}
	creds.AppleID = strings.TrimSpace(creds.AppleID)
	if creds.DeviceCodeURL != "" {
		normalized, err := normalizeDeviceCodeURL(creds.DeviceCodeURL)
		if err != nil {
			return err
		}
		creds.DeviceCodeURL = normalized
		creds.TwoFactor = "trusted-device"
	}
	return nil
}

func normalizeDeviceCodeURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("设备码接收链接必须是有效的公网 HTTPS 地址")
	}
	if literal, err := netip.ParseAddr(parsed.Hostname()); err == nil && !publicProviderIP(literal) {
		return "", errors.New("设备码接收链接不能指向本机或内网地址")
	}
	parsed.Scheme = "https"
	return parsed.String(), nil
}

func publicProviderIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, blocked := range blockedProviderNets {
		if blocked.Contains(address) {
			return false
		}
	}
	return true
}

func dialDeviceCodeProvider(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("设备码接口地址无效")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("设备码接口域名无法解析")
	}
	dialer := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 20 * time.Second}
	var lastErr error
	for _, candidate := range addresses {
		if !publicProviderIP(candidate) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("设备码接口解析到了非公网地址")
}

func newDeviceCodeHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialDeviceCodeProvider,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 8 * time.Second,
		IdleConnTimeout:     20 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	client.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 3 {
			return errors.New("设备码接口重定向过多")
		}
		_, err := normalizeDeviceCodeURL(request.URL.String())
		return err
	}
	return client
}

func parseDeviceCodeResponse(body []byte) (string, error) {
	matches := sixDigitCodePattern.FindAllSubmatch(body, -1)
	unique := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			unique[string(match[1])] = struct{}{}
		}
	}
	if len(unique) != 1 {
		return "", errors.New("设备码接口没有返回唯一的六位验证码")
	}
	for code := range unique {
		return code, nil
	}
	return "", errors.New("设备码接口没有返回验证码")
}

func fetchDeviceCode(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/plain, application/json")
	request.Header.Set("User-Agent", "AppleServices-Dashboard/1")
	response, err := newDeviceCodeHTTPClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("设备码接口暂时不可用")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, deviceCodeResponseLimit+1))
	if err != nil || len(body) > deviceCodeResponseLimit {
		return "", errors.New("设备码接口响应无效")
	}
	return parseDeviceCodeResponse(body)
}

func (s *Server) consumeDeviceCodeProvider(account *Account, dir, endpoint string, workerPID int) {
	ctx, cancel := context.WithTimeout(context.Background(), deviceCodePollTimeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	submitted := map[string]bool{}
	announced, observedWorker := false, false
	for {
		status := s.readStatusFrom(dir)
		if status.PID == workerPID && status.Running {
			observedWorker = true
		}
		if observedWorker && (status.PID != workerPID || !status.Running) {
			return
		}
		action := ""
		if observedWorker {
			switch status.State {
			case "awaiting_device_code":
				action = "submit-sms"
			case "awaiting_account_validation":
				action = "submit-account-code"
			case "online":
				return
			}
		}
		if action != "" {
			if !announced {
				s.setMessage(account, "正在等待设备码接口返回验证码")
				announced = true
			}
			code, err := fetchDeviceCode(ctx, endpoint)
			if err == nil && !submitted[code] {
				submitted[code] = true
				callCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				var result control.Result
				callErr := s.callFor(callCtx, dir, "/action", control.Action{Action: action, Code: code}, &result)
				stop()
				if callErr == nil && result.OK {
					s.setMessage(account, "设备验证码已自动提交，正在完成登录")
					return
				}
				if len(submitted) >= 2 {
					s.setMessage(account, "设备码接口返回的验证码未被 Apple 接受，请在面板手动输入新验证码")
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			if announced {
				s.setMessage(account, "设备码接口等待超时，请在面板手动输入验证码")
			}
			return
		case <-ticker.C:
		}
	}
}
