// Command nativeadicheck starts the native ADI bridge for one account
// directory (device.json + native-adi/hw_info.plist + push.json from
// tools/mac-push-check.py) and prints the SHAPE of the generated headers and
// six-digit codes without contacting any account API. Secret header values
// are redacted to their length and a short hash prefix.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/aps"
	"github.com/Laky-64/appleservices/nativeadi"
)

func main() {
	dir := flag.String("dir", "", "account directory")
	library := flag.String("adi-dll", "", "pinned native module")
	python := flag.String("python", "python3", "python")
	flag.Parse()
	fail := func(stage string, err error) { fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err); os.Exit(1) }
	var device appleservices.Device
	data, err := os.ReadFile(filepath.Join(*dir, "device.json"))
	if err != nil || json.Unmarshal(data, &device) != nil {
		fail("device", fmt.Errorf("device.json unreadable"))
	}
	var push aps.State
	data, err = os.ReadFile(filepath.Join(*dir, "push.json"))
	if err != nil || json.Unmarshal(data, &push) != nil {
		fail("push", fmt.Errorf("push.json unreadable"))
	}
	if err := nativeadi.ValidateLibrary(*library); err != nil {
		fail("validate", err)
	}
	if err := nativeadi.CheckRuntime(*python); err != nil {
		fail("runtime", err)
	}
	client, err := nativeadi.NewClient(nativeadi.Config{LibraryPath: *library, ProfileDir: filepath.Join(*dir, "native-adi"),
		Python: *python, Identifier: device.Identifier, Serial: device.SerialNumber, PushToken: push.Token})
	if err != nil {
		fail("new", err)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		fail("start", err)
	}
	defer client.Close()
	fmt.Printf("bridge ready in %s (device %x..., serial %s)\n", time.Since(start).Round(time.Millisecond), device.Identifier[:2], device.SerialNumber)
	headers, err := client.Headers()
	if err != nil {
		fail("headers", err)
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	public := map[string]bool{"X-Apple-I-MD-RINFO": true, "X-Mme-Client-Info": true, "User-Agent": true, "X-Apple-I-Client-Time": true, "X-Apple-I-TimeZone": true, "X-Apple-Locale": true, "X-Apple-I-Locale": true}
	for _, k := range keys {
		v := headers[k]
		if public[k] {
			fmt.Printf("  %-22s %s\n", k, v)
		} else {
			sum := sha256.Sum256([]byte(v))
			fmt.Printf("  %-22s <%d chars, sha256 %s…>\n", k, len(v), hex.EncodeToString(sum[:3]))
		}
	}
	for i := 0; i < 2; i++ {
		code, err := client.Code(ctx)
		if err != nil {
			fail("code", err)
		}
		fmt.Printf("code #%d: 6 digits ok (%c*****)\n", i+1, code[0])
	}
}
