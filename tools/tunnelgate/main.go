// Command tunnelgate is a temporary access gate for reaching the loopback-only
// dashboard through a reverse tunnel (for example a cloudflared quick tunnel)
// when an SSH tunnel is not practical.
//
// It listens on a unix socket (no extra TCP port), requires a one-time random
// access key (?k=...) that it turns into a Secure, HttpOnly, SameSite=Strict
// cookie, and forwards to the dashboard with Host and Origin rewritten to the
// dashboard's own loopback address, so the dashboard's Host/Origin/X-App-Token
// checks stay in force unchanged. Stop it (and the tunnel) when done.
//
//	tunnelgate -socket /path/gate.sock -upstream 127.0.0.1:18741 -key-file /path/key
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
)

const accessKeyBytes = 24

func loadOrCreateKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(data))
		decoded, decodeErr := hex.DecodeString(key)
		info, statErr := os.Stat(path)
		insecureMode := statErr == nil && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0
		if decodeErr != nil || len(decoded) != accessKeyBytes || key != strings.ToLower(key) || statErr != nil || insecureMode {
			return "", errors.New("invalid or insecure access key file")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, accessKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := hex.EncodeToString(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateKey(path)
	}
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(key + "\n"); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return key, nil
}

func main() {
	socket := flag.String("socket", "", "unix socket to listen on")
	upstream := flag.String("upstream", "127.0.0.1:18741", "dashboard loopback address")
	keyFile := flag.String("key-file", "", "file to write the random access key to (0600)")
	flag.Parse()
	if *socket == "" || *keyFile == "" {
		fmt.Fprintln(os.Stderr, "socket and key-file are required")
		os.Exit(2)
	}
	if host, _, err := net.SplitHostPort(*upstream); err != nil || host != "127.0.0.1" {
		fmt.Fprintln(os.Stderr, "upstream must be 127.0.0.1:<port>")
		os.Exit(2)
	}
	key, err := loadOrCreateKey(*keyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot write key file")
		os.Exit(1)
	}
	const cookie = "asgate"
	target := &url.URL{Scheme: "http", Host: *upstream}
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(r *http.Request) {
		base(r)
		r.Host = *upstream
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", "http://"+*upstream)
		}
		r.Header.Del("Referer")
		cookies := r.Cookies()
		r.Header.Del("Cookie")
		for _, value := range cookies {
			if value.Name != cookie {
				r.AddCookie(value)
			}
		}
		r.Header.Set("X-Forwarded-Proto", "https")
		for _, h := range []string{"X-Forwarded-For", "Cf-Connecting-Ip", "Cf-Ray", "Cf-Ipcountry", "Cf-Visitor", "Cdn-Loop"} {
			r.Header.Del(h)
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("X-Robots-Tag", "noindex")
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if k := r.URL.Query().Get("k"); k != "" {
			if subtle.ConstantTimeCompare([]byte(k), []byte(key)) != 1 {
				http.Error(w, "forbidden", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookie, Value: key, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(cookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(key)) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	os.Remove(*socket)
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot listen")
		os.Exit(1)
	}
	os.Chmod(*socket, 0600)
	fmt.Println("tunnelgate listening on", *socket)
	if err := http.Serve(listener, handler); err != nil {
		os.Exit(1)
	}
}
