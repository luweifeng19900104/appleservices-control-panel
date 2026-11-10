package dashboard

import (
	"strings"
	"testing"
)

func TestPrimaryFeatureCardsStayVisible(t *testing.T) {
	htmlData, err := assets.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlData)
	for _, required := range []string{
		"id=\"featureCenter\"",
		"id=\"activationCard\"",
		"id=\"familyControls\"",
		"id=\"hmeCard\"",
		"id=\"accountImport\"",
		"id=\"deviceCodeURL\"",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard is missing %s", required)
		}
	}
	for _, hidden := range []string{
		"id=\"activationCard\" hidden",
		"id=\"familyControls\" hidden",
		"id=\"hmeCard\" hidden",
	} {
		if strings.Contains(html, hidden) {
			t.Fatalf("primary feature is still conditionally absent: %s", hidden)
		}
	}
}

func TestFeatureRendererKeepsLockedCardsOnScreen(t *testing.T) {
	data, err := assets.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, forbidden := range []string{"card.hidden=!ready", "$('hmeCard').hidden=!hmeEligible"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("feature renderer still hides an unavailable card: %s", forbidden)
		}
	}
	for _, required := range []string{"renderFeatureCenter", "hmeAvailability", "device_code_url", "import_line"} {
		if !strings.Contains(script, required) {
			t.Fatalf("feature renderer is missing %s", required)
		}
	}
	for _, required := range []string{"store_password_ready", "store_authenticated", "storage-skill-auth-local", "先验证购买会话后才能提交订阅", "result.apple_code"} {
		if !strings.Contains(script, required) {
			t.Fatalf("Store purchase gating is missing %s", required)
		}
	}
}

func TestEveryOverviewCardReceivesTheEntryAnimation(t *testing.T) {
	data, err := assets.ReadFile("web/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(data)
	if strings.Contains(styles, "request-card.overview-only:nth-of-type") {
		t.Fatal("overview animation is still limited to hard-coded section positions")
	}
	if !strings.Contains(styles, "body.ui-ready main>.request-card.overview-only") {
		t.Fatal("overview cards have no general entry animation")
	}
}
