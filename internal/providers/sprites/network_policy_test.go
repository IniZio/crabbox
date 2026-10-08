package sprites

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestSpritesClientSetNetworkPolicy(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string][]map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.EscapedPath(), r.Method, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client, err := newSpritesClient(core.Config{Sprites: core.SpritesConfig{Token: "test-token", APIURL: srv.URL}}, core.Runtime{HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetNetworkPolicy(context.Background(), "crabbox-a b", []string{"github.com", "*.npmjs.org"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/sprites/crabbox-a%20b/policy/network" || gotAuth != "Bearer test-token" {
		t.Fatalf("method=%s path=%s auth=%q", gotMethod, gotPath, gotAuth)
	}
	want := []map[string]string{{"action": "allow", "domain": "github.com"}, {"action": "allow", "domain": "*.npmjs.org"}}
	if !reflect.DeepEqual(gotBody["rules"], want) {
		t.Fatalf("rules=%v", gotBody["rules"])
	}
}

func TestSpritesClientSetNetworkPolicyRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token test-token", http.StatusBadRequest)
	}))
	defer srv.Close()
	client, err := newSpritesClient(core.Config{Sprites: core.SpritesConfig{Token: "test-token", APIURL: srv.URL}}, core.Runtime{HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	err = client.SetNetworkPolicy(context.Background(), "s", []string{"github.com"})
	if err == nil || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeSpritesNetworkAllow(t *testing.T) {
	got, err := normalizeSpritesNetworkAllow([]string{" GitHub.com ", "", "github.com", "*.npmjs.org"})
	if err != nil || !reflect.DeepEqual(got, []string{"github.com", "*.npmjs.org"}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
	for _, bad := range []string{"1.1.1.1", "https://github.com", "github.com/x", "github.com:443", "*", "*.", "localhost", "a b.com", "::1"} {
		if _, err := normalizeSpritesNetworkAllow([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSpritesNetworkAllowValidatedBeforeBackend(t *testing.T) {
	cfg := core.Config{Sprites: core.SpritesConfig{Token: "test-token", WorkRoot: "/home/sprite/crabbox", NetworkAllow: []string{"8.8.8.8"}}}
	_, err := NewSpritesBackend(Provider{}.Spec(), cfg, core.Runtime{Stdout: io.Discard, Stderr: io.Discard, Exec: &recordingRunner{}})
	if err == nil || !strings.Contains(err.Error(), "network allow entry") {
		t.Fatalf("err=%v", err)
	}
}

func TestSpritesMissingTokenFailsBeforeAnyAPICall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	cfg := core.Config{Sprites: core.SpritesConfig{APIURL: srv.URL, WorkRoot: "/home/sprite/crabbox", NetworkAllow: []string{"github.com"}}}
	_, err := NewSpritesBackend(Provider{}.Spec(), cfg, core.Runtime{HTTP: srv.Client(), Stdout: io.Discard, Stderr: io.Discard, Exec: &recordingRunner{}})
	if err == nil || !strings.Contains(err.Error(), "requires SPRITES_TOKEN") || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestSpritesApplyNetworkPolicy(t *testing.T) {
	api := &fakeSpritesAPI{}
	b := &spritesBackend{client: api, rt: core.Runtime{Stderr: io.Discard}}
	if err := b.applyNetworkPolicy(context.Background(), "s1"); err != nil || len(api.policyDomains) != 0 {
		t.Fatalf("empty list should be a no-op: err=%v calls=%v", err, api.policyDomains)
	}
	b.cfg.Sprites.NetworkAllow = []string{"github.com"}
	if err := b.applyNetworkPolicy(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	// Re-apply a different list on the same sprite.
	b.cfg.Sprites.NetworkAllow = []string{"github.com", "example.com"}
	if err := b.applyNetworkPolicy(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"github.com"}, {"github.com", "example.com"}}
	if api.policyName != "s1" || !reflect.DeepEqual(api.policyDomains, want) {
		t.Fatalf("name=%q calls=%v", api.policyName, api.policyDomains)
	}
	api.policyErr = errors.New("boom")
	if err := b.applyNetworkPolicy(context.Background(), "s1"); err == nil || !strings.Contains(err.Error(), "set network policy") {
		t.Fatalf("err=%v", err)
	}
}
