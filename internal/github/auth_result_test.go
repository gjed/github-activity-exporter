package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// authResults collects the values passed to an AuthResultHook.
type authResults struct {
	mu  sync.Mutex
	got []bool
}

func (a *authResults) hook(unauthorized bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, unauthorized)
}

func (a *authResults) values() []bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bool(nil), a.got...)
}

// TestAuthResultHook_ReportsEveryResponse checks that the hook sees one call
// per response, true only for 401.
func TestAuthResultHook_ReportsEveryResponse(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	res := &authResults{}
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithAuthResultHook(res.hook),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()
	_, _, _ = c.REST().Repositories.Get(ctx, "o", "r")
	status = http.StatusOK
	_, _, _ = c.REST().Repositories.Get(ctx, "o", "r")
	status = http.StatusForbidden
	_, _, _ = c.REST().Repositories.Get(ctx, "o", "r")

	got := res.values()
	want := []bool{true, false, false}
	if len(got) != len(want) {
		t.Fatalf("hook calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hook calls = %v, want %v", got, want)
			break
		}
	}
}

// TestAuthResultHook_AppTokenExchange401 checks that a GitHub App whose
// installation token exchange is rejected with 401 reports unauthorized,
// and that the error wraps ErrUnauthorized.
func TestAuthResultHook_AppTokenExchange401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/2/access_tokens" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	_, keyPEM := generateTestRSAKey(t)
	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          1,
		InstallationID: 2,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator: %v", err)
	}

	res := &authResults{}
	c, err := internalgithub.NewClient(auth,
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithAuthResultHook(res.hook),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if _, _, err := c.REST().Repositories.Get(context.Background(), "o", "r"); err == nil {
		t.Fatal("expected an error from the rejected token exchange")
	}
	if got := res.values(); len(got) != 1 || !got[0] {
		t.Errorf("hook calls = %v, want [true]", got)
	}

	if _, err := auth.Token(context.Background()); !errors.Is(err, internalgithub.ErrUnauthorized) {
		t.Errorf("Token error = %v, want wrapping ErrUnauthorized", err)
	}
}
