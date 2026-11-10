package dashboard

import "testing"

func TestNormalizeImportedCredentials(t *testing.T) {
	creds := credentials{ImportLine: "owner@example.test----sample secret----https://codes.example.test:56133/api/get?id=42"}
	if err := normalizeCredentials(&creds); err != nil {
		t.Fatal(err)
	}
	if creds.AppleID != "owner@example.test" || creds.Password != "sample secret" || creds.TwoFactor != "trusted-device" || creds.DeviceCodeURL != "https://codes.example.test:56133/api/get?id=42" || creds.ImportLine != "" {
		t.Fatalf("unexpected imported credentials: %#v", creds)
	}
}

func TestNormalizeImportedCredentialsRejectsMalformedInput(t *testing.T) {
	for _, line := range []string{
		"owner@example.test----secret",
		"----secret----https://codes.example.test/get",
		"owner@example.test--------https://codes.example.test/get",
		"owner@example.test----secret----http://codes.example.test/get",
	} {
		creds := credentials{ImportLine: line}
		if err := normalizeCredentials(&creds); err == nil {
			t.Fatalf("accepted malformed import %q", line)
		}
	}
}

func TestNormalizeDeviceCodeURLRejectsLocalTargets(t *testing.T) {
	for _, target := range []string{
		"http://codes.example.test/get",
		"https://127.0.0.1/get",
		"https://10.0.0.4/get",
		"https://[::1]/get",
	} {
		if _, err := normalizeDeviceCodeURL(target); err == nil {
			t.Fatalf("accepted unsafe device-code URL %q", target)
		}
	}
	if _, err := normalizeDeviceCodeURL("https://codes.example.test:56133/api/get?id=42"); err != nil {
		t.Fatal(err)
	}
}

func TestParseDeviceCodeResponseRequiresOneSixDigitCode(t *testing.T) {
	for _, body := range []string{
		"AppleID login code:123456",
		`{"message":"AppleID login code: 123456"}`,
		"code=123456; repeated=123456",
	} {
		code, err := parseDeviceCodeResponse([]byte(body))
		if err != nil || code != "123456" {
			t.Fatalf("failed to parse %q: code=%q err=%v", body, code, err)
		}
	}
	for _, body := range []string{"no code", "12345", "first=123456 second=654321"} {
		if _, err := parseDeviceCodeResponse([]byte(body)); err == nil {
			t.Fatalf("accepted ambiguous response %q", body)
		}
	}
}
