package proxy

import (
	"io"
	"kiro-go/config"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingProfileArnUsesBuilderIDPlaceholder(t *testing.T) {
	account := &config.Account{Provider: "BuilderId"}

	if got := streamingProfileArn(account); got != builderIDProfileArn {
		t.Fatalf("expected Builder ID placeholder ARN %q, got %q", builderIDProfileArn, got)
	}
}

func TestStreamingProfileArnPrefersConfiguredArn(t *testing.T) {
	account := &config.Account{
		Provider:   "BuilderId",
		ProfileArn: " arn:aws:codewhisperer:us-east-1:123456789012:profile/REAL ",
	}

	if got := streamingProfileArn(account); got != "arn:aws:codewhisperer:us-east-1:123456789012:profile/REAL" {
		t.Fatalf("expected configured ARN, got %q", got)
	}
}

func TestManagementProfileArnSkipsBuilderIDPlaceholder(t *testing.T) {
	account := &config.Account{ProfileArn: builderIDProfileArn}

	if got := managementProfileArn(account); got != "" {
		t.Fatalf("expected placeholder ARN to be skipped, got %q", got)
	}
}

func TestWithProfileArnQuerySkipsBuilderIDPlaceholder(t *testing.T) {
	rawURL := "https://example.com/getUsageLimits?origin=AI_EDITOR"
	account := &config.Account{ProfileArn: builderIDProfileArn}

	if got := withProfileArnQuery(rawURL, account); got != rawURL {
		t.Fatalf("expected URL without placeholder ARN, got %q", got)
	}
}

func TestWithProfileArnQueryIncludesRealArn(t *testing.T) {
	rawURL := "https://example.com/getUsageLimits?origin=AI_EDITOR"
	realArn := "arn:aws:codewhisperer:us-east-1:123456789012:profile/REAL"
	account := &config.Account{ProfileArn: realArn}

	got := withProfileArnQuery(rawURL, account)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if gotArn := parsed.Query().Get("profileArn"); gotArn != realArn {
		t.Fatalf("expected real ARN %q, got %q", realArn, gotArn)
	}
}

func TestResolveProfileArnReturnsCachedValueWithoutRequest(t *testing.T) {
	kiroRestHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unexpected HTTP request for cached profile ARN")
			return nil, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	account := &config.Account{ProfileArn: " arn:aws:codewhisperer:profile/test "}
	got, err := ResolveProfileArn(account)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "arn:aws:codewhisperer:profile/test" {
		t.Fatalf("expected trimmed cached ARN, got %q", got)
	}
}

func TestResolveProfileArnFetchesAndCachesProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Init(configPath); err != nil {
		t.Fatalf("init config: %v", err)
	}
	account := config.Account{
		ID:           "acct-1",
		Email:        "user@example.com",
		AccessToken:  "access-token",
		Region:       "us-east-1",
		UsageCurrent: 7,
	}
	if err := config.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	kiroRestHttpStore.Store(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", req.Method)
			}
			if req.URL.Path != "/ListAvailableProfiles" {
				t.Fatalf("expected ListAvailableProfiles path, got %s", req.URL.Path)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("expected JSON content type, got %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"profiles":[{"arn":" arn:aws:codewhisperer:profile/fetched "}]} `)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	t.Cleanup(func() { InitKiroHttpClient("") })

	requestAccount := account
	requestAccount.UsageCurrent = 0
	got, err := ResolveProfileArn(&requestAccount)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "arn:aws:codewhisperer:profile/fetched" {
		t.Fatalf("expected fetched ARN, got %q", got)
	}
	if requestAccount.ProfileArn != got {
		t.Fatalf("expected account to be updated with fetched ARN, got %q", requestAccount.ProfileArn)
	}

	accounts := config.GetAccounts()
	if len(accounts) != 1 {
		t.Fatalf("expected one persisted account, got %d", len(accounts))
	}
	if accounts[0].ProfileArn != got {
		t.Fatalf("expected persisted account profile ARN %q, got %q", got, accounts[0].ProfileArn)
	}
	if accounts[0].UsageCurrent != 7 {
		t.Fatalf("expected profile cache update to preserve usage fields, got usageCurrent=%v", accounts[0].UsageCurrent)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
