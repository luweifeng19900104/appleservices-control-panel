package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dashboardRequest(server *Server, method, path string, body []byte, authToken string, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, server.URL()+path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authToken != "" {
		request.Header.Set("X-Auth-Token", authToken)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	return request
}

func TestDashboardFirstRunPasswordAndPersistentLogin(t *testing.T) {
	dir := t.TempDir()
	server, err := New(Config{Dir: dir, Port: 0})
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodGet, "/", nil, "", nil))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/auth" {
		t.Fatal("first dashboard visit did not redirect to password setup")
	}

	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodGet, "/auth", nil, "", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), server.authToken) || strings.Contains(recorder.Body.String(), server.token) {
		t.Fatal("authentication page leaked the dashboard capability or lost its own token")
	}

	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodGet, "/auth/status", nil, server.authToken, nil))
	var status map[string]bool
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &status) != nil || status["configured"] || status["authenticated"] {
		t.Fatal("fresh authentication state was not reported")
	}

	shortBody, _ := json.Marshal(map[string]string{"password": "short", "confirm": "short"})
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodPost, "/auth/setup", shortBody, server.authToken, nil))
	if recorder.Code != http.StatusBadRequest || server.auth.configured() {
		t.Fatal("weak first-run password was accepted")
	}

	const password = "correct-horse-battery-staple"
	setupBody, _ := json.Marshal(map[string]string{"password": password, "confirm": password})
	setupRequest := dashboardRequest(server, http.MethodPost, "/auth/setup", setupBody, server.authToken, nil)
	setupRequest.Header.Set("X-Forwarded-Proto", "https")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, setupRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("password setup failed: %s", recorder.Body.String())
	}
	response := recorder.Result()
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == dashboardSessionCookie {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode {
		t.Fatal("password setup did not issue a hardened session cookie")
	}

	stored, err := os.ReadFile(filepath.Join(dir, dashboardAuthFile))
	if err != nil || bytes.Contains(stored, []byte(password)) {
		t.Fatal("dashboard password was absent or stored in plaintext")
	}

	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodGet, "/", nil, "", session))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), server.token) || !strings.Contains(recorder.Body.String(), server.authToken) {
		t.Fatal("authenticated dashboard did not receive its capabilities")
	}

	logoutRequest := dashboardRequest(server, http.MethodPost, "/auth/logout", nil, server.authToken, session)
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, logoutRequest)
	if recorder.Code != http.StatusOK {
		t.Fatal("dashboard logout failed")
	}
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, dashboardRequest(server, http.MethodGet, "/", nil, "", session))
	if recorder.Code != http.StatusSeeOther {
		t.Fatal("logged-out dashboard session remained valid")
	}

	server.listener.Close()
	reopened, err := New(Config{Dir: dir, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.listener.Close()
	if !reopened.auth.configured() {
		t.Fatal("dashboard password did not persist across restart")
	}

	wrongBody, _ := json.Marshal(map[string]string{"password": "incorrect-password"})
	recorder = httptest.NewRecorder()
	reopened.ServeHTTP(recorder, dashboardRequest(reopened, http.MethodPost, "/auth/login", wrongBody, reopened.authToken, nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatal("incorrect dashboard password was accepted")
	}
	loginBody, _ := json.Marshal(map[string]string{"password": password})
	recorder = httptest.NewRecorder()
	reopened.ServeHTTP(recorder, dashboardRequest(reopened, http.MethodPost, "/auth/login", loginBody, reopened.authToken, nil))
	if recorder.Code != http.StatusOK || len(recorder.Result().Cookies()) == 0 {
		t.Fatal("persisted dashboard password could not create a new session")
	}
}

func TestDashboardPasswordMinimumLength(t *testing.T) {
	if validateDashboardPassword("123456789") == nil {
		t.Fatal("nine-character dashboard password was accepted")
	}
	if err := validateDashboardPassword("1234567890"); err != nil {
		t.Fatalf("ten-character dashboard password was rejected: %v", err)
	}
}
