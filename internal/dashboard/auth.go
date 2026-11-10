package dashboard

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	dashboardAuthFile       = "dashboard-auth.json"
	dashboardAuthVersion    = 1
	dashboardAuthIterations = 600_000
	dashboardSessionCookie  = "appleservices_dashboard_session"
	dashboardSessionTTL     = 24 * time.Hour
)

var errDashboardAuthBlocked = errors.New("dashboard authentication temporarily blocked")

type passwordRecord struct {
	Version    int    `json:"version"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Hash       string `json:"password_hash"`
	CreatedAt  string `json:"created_at"`
}

type dashboardAuth struct {
	path         string
	mu           sync.Mutex
	record       *passwordRecord
	sessions     map[[sha256.Size]byte]time.Time
	failures     int
	blockedUntil time.Time
}

func loadDashboardAuth(dir string) (*dashboardAuth, error) {
	auth := &dashboardAuth{path: filepath.Join(dir, dashboardAuthFile), sessions: make(map[[sha256.Size]byte]time.Time)}
	data, err := os.ReadFile(auth.path)
	if errors.Is(err, os.ErrNotExist) {
		return auth, nil
	}
	if err != nil {
		return nil, errors.New("dashboard authentication state is unavailable")
	}
	var record passwordRecord
	if len(data) > 4096 || json.Unmarshal(data, &record) != nil || validatePasswordRecord(record) != nil {
		return nil, errors.New("dashboard authentication state is invalid")
	}
	auth.record = &record
	return auth, nil
}

func validatePasswordRecord(record passwordRecord) error {
	salt, saltErr := base64.StdEncoding.DecodeString(record.Salt)
	hash, hashErr := base64.StdEncoding.DecodeString(record.Hash)
	if record.Version != dashboardAuthVersion || record.Iterations < 100_000 || record.Iterations > 2_000_000 || saltErr != nil || hashErr != nil || len(salt) != 32 || len(hash) != sha256.Size {
		return errors.New("invalid password record")
	}
	return nil
}

func validateDashboardPassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < 10 {
		return errors.New("管理密码至少需要 10 个字符")
	}
	if length > 128 || len(password) > 512 {
		return errors.New("管理密码过长")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("管理密码不能只包含空白字符")
	}
	return nil
}

func deriveDashboardPassword(password string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
}

func (a *dashboardAuth) configured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.record != nil
}

func (a *dashboardAuth) configure(password string) error {
	if err := validateDashboardPassword(password); err != nil {
		return err
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return errors.New("无法生成管理密码凭据")
	}
	derived, err := deriveDashboardPassword(password, salt, dashboardAuthIterations)
	if err != nil {
		return errors.New("无法生成管理密码凭据")
	}
	record := passwordRecord{
		Version:    dashboardAuthVersion,
		Iterations: dashboardAuthIterations,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Hash:       base64.StdEncoding.EncodeToString(derived),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return errors.New("无法保存管理密码")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record != nil {
		return errors.New("管理密码已经设置")
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return errors.New("无法保存管理密码")
	}
	temporary, err := os.CreateTemp(filepath.Dir(a.path), ".dashboard-auth-*.tmp")
	if err != nil {
		return errors.New("无法保存管理密码")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return errors.New("无法保存管理密码")
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return errors.New("无法保存管理密码")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return errors.New("无法保存管理密码")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("无法保存管理密码")
	}
	if err := os.Rename(temporaryPath, a.path); err != nil {
		return errors.New("无法保存管理密码")
	}
	a.record = &record
	a.failures = 0
	a.blockedUntil = time.Time{}
	return nil
}

func (a *dashboardAuth) verify(password string) (bool, error) {
	now := time.Now()
	a.mu.Lock()
	if now.Before(a.blockedUntil) {
		a.mu.Unlock()
		return false, errDashboardAuthBlocked
	}
	if a.record == nil {
		a.mu.Unlock()
		return false, errors.New("dashboard password is not configured")
	}
	record := *a.record
	a.mu.Unlock()

	salt, _ := base64.StdEncoding.DecodeString(record.Salt)
	expected, _ := base64.StdEncoding.DecodeString(record.Hash)
	derived, err := deriveDashboardPassword(password, salt, record.Iterations)
	if err != nil {
		return false, errors.New("dashboard password verification failed")
	}
	accepted := subtle.ConstantTimeCompare(derived, expected) == 1

	a.mu.Lock()
	defer a.mu.Unlock()
	if accepted {
		a.failures = 0
		a.blockedUntil = time.Time{}
		return true, nil
	}
	a.failures++
	if a.failures >= 5 {
		a.failures = 0
		a.blockedUntil = time.Now().Add(30 * time.Second)
	}
	return false, nil
}

func (a *dashboardAuth) newSession() (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, errors.New("dashboard session generation failed")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(dashboardSessionTTL)
	digest := sha256.Sum256([]byte(token))
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, deadline := range a.sessions {
		if !deadline.After(now) {
			delete(a.sessions, key)
		}
	}
	a.sessions[digest] = expires
	return token, expires, nil
}

func (a *dashboardAuth) authenticated(request *http.Request) bool {
	cookie, err := request.Cookie(dashboardSessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[digest]
	if !ok || !expires.After(time.Now()) {
		delete(a.sessions, digest)
		return false
	}
	return true
}

func (a *dashboardAuth) removeSession(request *http.Request) {
	cookie, err := request.Cookie(dashboardSessionCookie)
	if err != nil || cookie.Value == "" {
		return
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	delete(a.sessions, digest)
	a.mu.Unlock()
}

func dashboardCookieSecure(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	forwarded := strings.TrimSpace(strings.Split(request.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwarded, "https")
}

func setDashboardSessionCookie(writer http.ResponseWriter, request *http.Request, token string, expires time.Time) {
	http.SetCookie(writer, &http.Cookie{
		Name:     dashboardSessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(dashboardSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   dashboardCookieSecure(request),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearDashboardSessionCookie(writer http.ResponseWriter, request *http.Request) {
	http.SetCookie(writer, &http.Cookie{
		Name:     dashboardSessionCookie,
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   dashboardCookieSecure(request),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) authRequestAllowed(request *http.Request) bool {
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Auth-Token")), []byte(s.authToken)) != 1 {
		return false
	}
	origin := request.Header.Get("Origin")
	return origin == "" || origin == s.URL()
}

func (s *Server) serveAuth(writer http.ResponseWriter, request *http.Request) bool {
	switch request.URL.Path {
	case "/auth":
		if request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return true
		}
		data, err := assets.ReadFile("web/auth.html")
		if err != nil {
			http.NotFound(writer, request)
			return true
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		data = bytes.ReplaceAll(data, []byte("__AUTH_TOKEN__"), []byte(s.authToken))
		_, _ = writer.Write(data)
		return true
	case "/auth.js", "/auth.css":
		if request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return true
		}
		name := strings.TrimPrefix(request.URL.Path, "/")
		data, err := assets.ReadFile("web/" + name)
		if err != nil {
			http.NotFound(writer, request)
			return true
		}
		if strings.HasSuffix(name, ".js") {
			writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		} else {
			writer.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		_, _ = writer.Write(data)
		return true
	case "/auth/status", "/auth/setup", "/auth/login", "/auth/logout":
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !s.authRequestAllowed(request) {
			http.Error(writer, `{"message":"请刷新登录页面后重试"}`, http.StatusForbidden)
			return true
		}
	default:
		return false
	}

	switch request.URL.Path {
	case "/auth/status":
		if request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return true
		}
		_ = json.NewEncoder(writer).Encode(map[string]bool{"configured": s.auth.configured(), "authenticated": s.auth.authenticated(request)})
	case "/auth/setup":
		if request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return true
		}
		var input struct {
			Password string `json:"password"`
			Confirm  string `json:"confirm"`
		}
		if !readJSON(writer, request, &input) {
			return true
		}
		if input.Password != input.Confirm {
			http.Error(writer, `{"message":"两次输入的管理密码不一致"}`, http.StatusBadRequest)
			return true
		}
		err := s.auth.configure(input.Password)
		input.Password, input.Confirm = "", ""
		if err != nil {
			status := http.StatusBadRequest
			if s.auth.configured() {
				status = http.StatusConflict
			}
			writer.WriteHeader(status)
			_ = json.NewEncoder(writer).Encode(map[string]string{"message": err.Error()})
			return true
		}
		token, expires, err := s.auth.newSession()
		if err != nil {
			http.Error(writer, `{"message":"无法创建登录会话"}`, http.StatusInternalServerError)
			return true
		}
		setDashboardSessionCookie(writer, request, token, expires)
		_ = json.NewEncoder(writer).Encode(map[string]bool{"ok": true})
	case "/auth/login":
		if request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return true
		}
		var input struct {
			Password string `json:"password"`
		}
		if !readJSON(writer, request, &input) {
			return true
		}
		accepted, err := s.auth.verify(input.Password)
		input.Password = ""
		if errors.Is(err, errDashboardAuthBlocked) {
			writer.Header().Set("Retry-After", "30")
			http.Error(writer, `{"message":"登录尝试过多，请 30 秒后重试"}`, http.StatusTooManyRequests)
			return true
		}
		if err != nil || !accepted {
			time.Sleep(250 * time.Millisecond)
			http.Error(writer, `{"message":"管理密码错误"}`, http.StatusUnauthorized)
			return true
		}
		token, expires, err := s.auth.newSession()
		if err != nil {
			http.Error(writer, `{"message":"无法创建登录会话"}`, http.StatusInternalServerError)
			return true
		}
		setDashboardSessionCookie(writer, request, token, expires)
		_ = json.NewEncoder(writer).Encode(map[string]bool{"ok": true})
	case "/auth/logout":
		if request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return true
		}
		s.auth.removeSession(request)
		clearDashboardSessionCookie(writer, request)
		_ = json.NewEncoder(writer).Encode(map[string]bool{"ok": true})
	}
	return true
}
