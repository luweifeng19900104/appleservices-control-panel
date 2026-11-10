package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/Laky-64/appleservices/internal/dashboard"
)

func absOrEmpty(path string) string {
	if path == "" {
		return ""
	}
	abs, _ := filepath.Abs(path)
	return abs
}

func main() {
	dir := flag.String("dir", "./state", "saved device and session directory")
	worker := flag.String("worker", defaultWorker, "trusted-device executable")
	library := flag.String("adi-dll", defaultLibrary, "verified native module")
	python := flag.String("python", defaultPython, "64-bit Python executable")
	macPushSetup := flag.String("mac-push-setup", defaultMacPushSetup, "account-isolated Mac APS bootstrap")
	credentialKey := flag.String("credential-key", defaultCredentialKey, "32-byte key for encrypted reconnect credentials")
	nativeADI := flag.Bool("native-adi", defaultNativeADI, "start account workers with the native ADI module (Windows x64 only)")
	codeProvider := flag.String("code-provider", "", "absolute path of an external local ADI code-provider for workers started without -native-adi")
	port := flag.Int("port", 18741, "loopback dashboard port")
	flag.Parse()
	absDir, _ := filepath.Abs(*dir)
	absWorker, _ := filepath.Abs(*worker)
	ctx, cancel := signal.NotifyContext(context.Background(), stopSignals...)
	defer cancel()
	server, err := dashboard.New(dashboard.Config{Dir: absDir, Worker: absWorker, Library: absOrEmpty(*library), Python: *python,
		MacPushSetup: absOrEmpty(*macPushSetup), CredentialKeyFile: absOrEmpty(*credentialKey), Port: *port, NativeADI: *nativeADI, CodeProvider: *codeProvider})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	appleID, password := os.Getenv("APPLESERVICES_APPLEID"), os.Getenv("APPLESERVICES_PASSWORD")
	os.Unsetenv("APPLESERVICES_APPLEID")
	os.Unsetenv("APPLESERVICES_PASSWORD")
	server.Bootstrap(ctx, appleID, password)
	fmt.Println("Local dashboard:", server.URL())
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Dashboard stopped")
		os.Exit(1)
	}
}
