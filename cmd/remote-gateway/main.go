package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:18742", "TLS listen address")
	targetText := flag.String("target", "http://127.0.0.1:18741", "loopback dashboard URL")
	cert := flag.String("tls-cert", "", "PEM TLS certificate")
	key := flag.String("tls-key", "", "PEM TLS private key")
	flag.Parse()

	target, err := url.Parse(*targetText)
	if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.Port() == "" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		log.Fatal("remote gateway target must be an exact loopback HTTP origin")
	}
	if *cert == "" || *key == "" {
		log.Fatal("remote gateway requires TLS files")
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Host = target.Host
		request.Header.Del("Authorization")
		request.Header.Del("X-Forwarded-For")
		request.Header.Del("Origin")
		request.Header.Del("Referer")
		request.Header.Set("X-Forwarded-Proto", "https")
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "local dashboard unavailable", http.StatusBadGateway)
	}

	server := &http.Server{
		Addr:              *listen,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      4 * time.Minute,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ErrorLog:          log.New(os.Stderr, "remote-gateway: ", log.LstdFlags),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	log.Printf("AppleServices remote gateway listening on https://%s", *listen)
	if err := server.ListenAndServeTLS(*cert, *key); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(fmt.Errorf("remote gateway stopped: %w", err))
	}
}
