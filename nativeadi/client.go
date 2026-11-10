package nativeadi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const LibrarySHA256 = "ca88e816dda449d7ba24d54a5ed709a2d392daeae28c25a61dcd2e7496e44cfe"

func bridgeEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "APPLESERVICES_APPLEID") && !strings.EqualFold(key, "APPLESERVICES_PASSWORD") {
			result = append(result, entry)
		}
	}
	return result
}

type Config struct {
	LibraryPath string
	ProfileDir  string
	Python      string
	Identifier  []byte
	Serial      string
	PushToken   []byte
}

type reply struct {
	ID       uint64            `json:"id"`
	DeviceID string            `json:"device_id"`
	Ready    bool              `json:"ready"`
	Headers  map[string]string `json:"headers"`
	Code     string            `json:"code"`
	Error    string            `json:"error"`
}

// Client obtains headers and user verification codes from the same locally
// provisioned machine. It never receives an account password or service token.
type Client struct {
	config   Config
	deviceID uuid.UUID
	mu       sync.Mutex
	sequence uint64
	command  *exec.Cmd
	input    io.WriteCloser
	replies  chan reply
	done     chan struct{}
	closed   bool
}

func NewClient(config Config) (*Client, error) {
	if err := ValidateLibrary(config.LibraryPath); err != nil {
		return nil, err
	}
	identifier, err := uuid.FromBytes(config.Identifier)
	if err != nil || strings.TrimSpace(config.Serial) == "" || len(config.PushToken) != 32 {
		return nil, errors.New("nativeadi: complete device identity and genuine APS token are required")
	}
	profile, err := filepath.Abs(config.ProfileDir)
	if err != nil || config.ProfileDir == "" {
		return nil, errors.New("nativeadi: profile directory is required")
	}
	config.ProfileDir = profile
	if config.Python == "" {
		config.Python = "python"
	}
	python, err := exec.LookPath(config.Python)
	if err != nil {
		return nil, errors.New("nativeadi: 64-bit Python is unavailable")
	}
	config.Python = python
	return &Client{config: config, deviceID: identifier, replies: make(chan reply, 4), done: make(chan struct{})}, nil
}

func (c *Client) receive(ctx context.Context, id uint64) (reply, error) {
	select {
	case message := <-c.replies:
		if message.Error != "" {
			return reply{}, errors.New("nativeadi: native generation or provisioning failed")
		}
		identity, err := uuid.Parse(message.DeviceID)
		if err != nil || identity != c.deviceID || message.ID != id {
			return reply{}, errors.New("nativeadi: response used a different device or request")
		}
		return message, nil
	case <-c.done:
		return reply{}, errors.New("nativeadi: helper exited before completing the request")
	case <-ctx.Done():
		return reply{}, ctx.Err()
	}
}

func (c *Client) request(ctx context.Context, operation string) (reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.command == nil || c.closed {
		return reply{}, errors.New("nativeadi: bridge is not running")
	}
	c.sequence++
	if err := json.NewEncoder(c.input).Encode(map[string]any{"id": c.sequence, "op": operation}); err != nil {
		return reply{}, errors.New("nativeadi: could not send native request")
	}
	message, err := c.receive(ctx, c.sequence)
	if err != nil {
		c.input.Close()
		c.command.Process.Kill()
		c.closed = true
	}
	return message, err
}

func (c *Client) Headers() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	message, err := c.request(ctx, "headers")
	if err != nil {
		return nil, err
	}
	identity, err := uuid.Parse(message.Headers["X-Mme-Device-Id"])
	if err != nil || identity != c.deviceID {
		return nil, errors.New("nativeadi: authentication headers use a different device")
	}
	for _, key := range []string{"X-Apple-I-MD", "X-Apple-I-MD-M", "X-Apple-I-MD-RINFO", "X-Mme-Client-Info"} {
		if message.Headers[key] == "" {
			return nil, fmt.Errorf("nativeadi: authentication header %s is missing", key)
		}
	}
	// The ADI module reports the machine identifier it derived, but not the
	// lookup value that ties it to this device. Match the MDLU derivation used
	// by our remote anisette provider; this does not establish why a particular
	// delegate request was refused.
	if message.Headers["X-Apple-I-MD-LU"] == "" {
		if os.Getenv("APPLESERVICES_ADI_NO_MDLU") == "" {
			sum := sha256.Sum256(c.config.Identifier)
			message.Headers["X-Apple-I-MD-LU"] = hex.EncodeToString(sum[:])
		}
	}
	message.Headers["X-Apple-I-SRL-NO"] = c.config.Serial
	message.Headers["User-Agent"] = "akd/1.0 CFNetwork/1408.0.4 Darwin/22.5.0"
	// X-Apple-I-MD-RINFO is a routing/provisioning state. Known-good clients
	// send different values. An override is diagnostic only; changing this
	// number does not regenerate the associated provisioning credentials.
	if override := strings.TrimSpace(os.Getenv("APPLESERVICES_ADI_RINFO")); override != "" {
		message.Headers["X-Apple-I-MD-RINFO"] = override
	}
	return message.Headers, nil
}

func (c *Client) Code(ctx context.Context) (string, error) {
	message, err := c.request(ctx, "code")
	if err != nil {
		return "", err
	}
	if len(message.Code) != 6 || strings.IndexFunc(message.Code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", errors.New("nativeadi: no valid six-digit output")
	}
	return message.Code, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	command, input := c.command, c.input
	c.mu.Unlock()
	if command == nil {
		return nil
	}
	input.Close()
	select {
	case <-c.done:
		return nil
	case <-time.After(5 * time.Second):
		command.Process.Kill()
		return errors.New("nativeadi: helper shutdown timed out")
	}
}
