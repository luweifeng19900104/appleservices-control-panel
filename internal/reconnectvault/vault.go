package reconnectvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	FileName        = "reconnect-credentials.enc"
	formatVersion   = 1
	maximumFileSize = 64 << 10
)

type Credentials struct {
	Password      string `json:"password"`
	DeviceCodeURL string `json:"device_code_url,omitempty"`
	TwoFactor     string `json:"twofactor,omitempty"`
}

type envelope struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Vault struct {
	key [32]byte
}

func LoadKey(path string) (*Vault, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("credential key path is empty")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 32 {
		return nil, errors.New("credential key is unavailable")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0027 != 0 {
		return nil, errors.New("credential key permissions are unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 32 {
		return nil, errors.New("credential key is unavailable")
	}
	var key [32]byte
	copy(key[:], data)
	return &Vault{key: key}, nil
}

func associatedData(appleID string) []byte {
	return []byte("appleservices-reconnect-v1\x00" + strings.ToLower(strings.TrimSpace(appleID)))
}

func (v *Vault) aead() (cipher.AEAD, error) {
	if v == nil {
		return nil, errors.New("credential vault is unavailable")
	}
	block, err := aes.NewCipher(v.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validate(creds Credentials) error {
	if creds.Password == "" || len(creds.Password) > 4096 || len(creds.DeviceCodeURL) > 8192 || len(creds.TwoFactor) > 64 {
		return errors.New("invalid reconnect credentials")
	}
	return nil
}

func (v *Vault) Save(accountDir, appleID string, creds Credentials) error {
	if err := validate(creds); err != nil {
		return err
	}
	aead, err := v.aead()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, plain, associatedData(appleID))
	record, err := json.Marshal(envelope{Version: formatVersion, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(accountDir, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(accountDir, ".reconnect-credentials-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(record); err != nil {
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
	target := filepath.Join(accountDir, FileName)
	if err := os.Rename(temporaryPath, target); err != nil {
		if runtime.GOOS != "windows" {
			return err
		}
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(temporaryPath, target); err != nil {
			return err
		}
	}
	return os.Chmod(target, 0600)
}

func (v *Vault) Load(accountDir, appleID string) (Credentials, error) {
	path := filepath.Join(accountDir, FileName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumFileSize {
		return Credentials{}, errors.New("saved reconnect credentials are unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, errors.New("saved reconnect credentials are unavailable")
	}
	var record envelope
	if json.Unmarshal(data, &record) != nil || record.Version != formatVersion {
		return Credentials{}, errors.New("saved reconnect credentials are invalid")
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(record.Nonce)
	sealed, sealedErr := base64.RawStdEncoding.DecodeString(record.Ciphertext)
	aead, err := v.aead()
	if nonceErr != nil || sealedErr != nil || err != nil || len(nonce) != aead.NonceSize() {
		return Credentials{}, errors.New("saved reconnect credentials are invalid")
	}
	plain, err := aead.Open(nil, nonce, sealed, associatedData(appleID))
	if err != nil {
		return Credentials{}, errors.New("saved reconnect credentials do not match this account")
	}
	var creds Credentials
	if json.Unmarshal(plain, &creds) != nil || validate(creds) != nil {
		return Credentials{}, errors.New("saved reconnect credentials are invalid")
	}
	return creds, nil
}

func (v *Vault) Available(accountDir, appleID string) bool {
	_, err := v.Load(accountDir, appleID)
	return err == nil
}

func (v *Vault) Delete(accountDir string) error {
	err := os.Remove(filepath.Join(accountDir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
