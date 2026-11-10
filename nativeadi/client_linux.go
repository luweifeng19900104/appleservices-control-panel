//go:build linux

package nativeadi

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Laky-64/appleservices/internal/backgroundexec"
)

// Linux implementation of the native ADI bridge. Same stdin/stdout JSON
// protocol and native function numbers as the Windows bridge; the module is
// the official OpenBubbles Linux build, loaded with ctypes.CDLL, and the
// helper's own APS connection is refused by an LD_PRELOAD connect() shim so
// real push stays on the Go transport (aps/client.go).

// LinuxLibrarySHA256 pins lib/librust_lib_bluebubbles.so from the official
// OpenBubbles release v1.15.0+205, asset bluebubbles-linux-x86_64.tar
// (tar sha256 5584a4e79dbd22fcf19b51fb6ff6426d7f132555677e1ae9d3421f04657b563a).
// It exposes the same generated interface (content hash -208244658) as the
// pinned Windows module. It is downloaded at setup time, never committed.
const LinuxLibrarySHA256 = "ce260415598b78c0810ae6b917145fbca804f6f19385ca9d34d55921d38210cf"

// CheckRuntime checks only local Python and compiler prerequisites.
func CheckRuntime(python string) error {
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath("gcc"); err != nil {
		if _, err := exec.LookPath("cc"); err != nil {
			return errors.New("nativeadi: gcc is required to build the transport isolation shim")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", "import struct,sys; sys.exit(0 if struct.calcsize('P') == 8 and sys.version_info >= (3,10) else 1)")
	backgroundexec.Configure(command)
	command.Env = bridgeEnvironment()
	if err := command.Run(); err != nil {
		return errors.New("nativeadi: 64-bit Python 3.10 or newer is required")
	}
	return nil
}

//go:embed helper.py
var helperSource []byte

//go:embed bridge.py
var bridgeSource []byte

//go:embed assets/transport_fence_linux.c
var fenceSource []byte

func ValidateLibrary(path string) error {
	if runtime.GOARCH != "amd64" {
		return errors.New("nativeadi: Linux x86_64 is required")
	}
	if !filepath.IsAbs(path) {
		return errors.New("nativeadi: library path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("nativeadi: verified OpenBubbles Linux library is unavailable")
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 128*1024*1024)); err != nil {
		return errors.New("nativeadi: library hash read failed")
	}
	if hex.EncodeToString(hash.Sum(nil)) != LinuxLibrarySHA256 {
		return errors.New("nativeadi: library does not match the pinned OpenBubbles v1.15.0+205 Linux release")
	}
	return nil
}

func compiler() (string, error) {
	if path, err := exec.LookPath("gcc"); err == nil {
		return path, nil
	}
	return exec.LookPath("cc")
}

func (c *Client) Start(ctx context.Context) error {
	if c.command != nil || c.closed {
		return errors.New("nativeadi: client cannot be started again")
	}
	if err := os.MkdirAll(c.config.ProfileDir, 0700); err != nil {
		return errors.New("nativeadi: bridge directory unavailable")
	}
	directory, err := os.MkdirTemp(c.config.ProfileDir, "bridge-")
	if err != nil {
		return errors.New("nativeadi: bridge directory unavailable")
	}
	started := false
	defer func() {
		if !started {
			os.RemoveAll(directory)
		}
	}()
	for name, source := range map[string][]byte{"helper.py": helperSource, "bridge.py": bridgeSource, "transport_fence_linux.c": fenceSource} {
		if err := os.WriteFile(filepath.Join(directory, name), source, 0600); err != nil {
			return errors.New("nativeadi: could not save bridge source")
		}
	}
	fence := filepath.Join(directory, "transport_fence.so")
	cc, err := compiler()
	if err != nil {
		return errors.New("nativeadi: gcc is required to build the local transport isolation shim")
	}
	build := exec.CommandContext(ctx, cc, "-shared", "-fPIC", "-O2", filepath.Join(directory, "transport_fence_linux.c"), "-o", fence, "-ldl")
	backgroundexec.Configure(build)
	build.Env = bridgeEnvironment()
	if err := build.Run(); err != nil {
		return errors.New("nativeadi: transport isolation shim build failed")
	}
	command := exec.Command(c.config.Python, "-u", filepath.Join(directory, "helper.py"))
	backgroundexec.Configure(command)
	// The helper needs no account secrets, including through inherited env vars.
	// The fence is preloaded into this one process only.
	command.Env = append(withoutPreload(bridgeEnvironment()), "LD_PRELOAD="+fence)
	input, err := command.StdinPipe()
	if err != nil {
		return errors.New("nativeadi: bridge input unavailable")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return errors.New("nativeadi: bridge output unavailable")
	}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return errors.New("nativeadi: bridge could not start")
	}
	c.command, c.input = command, input
	started = true
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			var message reply
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				break
			}
			select {
			case c.replies <- message:
			default:
				input.Close()
			}
		}
		input.Close()
		command.Wait()
		os.RemoveAll(directory)
		close(c.done)
	}()
	init := map[string]any{"library": c.config.LibraryPath, "fence": fence, "profile": c.config.ProfileDir,
		"identifier": c.config.Identifier, "serial": c.config.Serial, "push_token": c.config.PushToken,
		"mac_address": SyntheticMACAddress(c.config.Identifier)}
	if err := json.NewEncoder(input).Encode(init); err != nil {
		c.Close()
		return errors.New("nativeadi: bridge initialization failed")
	}
	message, err := c.receive(ctx, 0)
	if err != nil || !message.Ready {
		c.Close()
		if err == nil {
			err = errors.New("nativeadi: generation preflight did not complete")
		}
		return err
	}
	return nil
}

func withoutPreload(env []string) []string {
	result := env[:0:0]
	for _, entry := range env {
		if len(entry) >= 11 && entry[:11] == "LD_PRELOAD=" {
			continue
		}
		result = append(result, entry)
	}
	return result
}
