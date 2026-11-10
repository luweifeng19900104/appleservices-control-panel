// Command reconnectvault saves one account's reconnect password and optional
// device-code URL through the same encrypted store used by the dashboard.
// The password and URL are read as two stdin lines and are never accepted as
// command-line flags, so they do not appear in process listings.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Laky-64/appleservices/internal/reconnectvault"
)

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

func run() error {
	key := flag.String("key", "", "32-byte credential key")
	dir := flag.String("dir", "", "account state directory")
	appleID := flag.String("apple-id", "", "account binding")
	twoFactor := flag.String("twofactor", "trusted-device", "saved verification mode")
	deleteSaved := flag.Bool("delete", false, "delete saved reconnect credentials")
	flag.Parse()
	if *key == "" || *dir == "" || strings.TrimSpace(*appleID) == "" {
		return fmt.Errorf("key, dir and apple-id are required")
	}
	vault, err := reconnectvault.LoadKey(*key)
	if err != nil {
		return err
	}
	if *deleteSaved {
		return vault.Delete(*dir)
	}
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 16<<10))
	password, err := readLine(reader)
	if err != nil || password == "" {
		return fmt.Errorf("password input is required")
	}
	deviceCodeURL, err := readLine(reader)
	if err != nil {
		return fmt.Errorf("device-code URL input is invalid")
	}
	if deviceCodeURL != "" && !strings.HasPrefix(strings.ToLower(deviceCodeURL), "https://") {
		return fmt.Errorf("device-code URL must use HTTPS")
	}
	if deviceCodeURL != "" {
		*twoFactor = "trusted-device"
	}
	if err := vault.Save(*dir, *appleID, reconnectvault.Credentials{Password: password, DeviceCodeURL: deviceCodeURL, TwoFactor: *twoFactor}); err != nil {
		return err
	}
	fmt.Println("encrypted reconnect credentials saved")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reconnect credential operation failed")
		os.Exit(1)
	}
}
