package cliproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type unlistedCatalogHarness struct {
	t        *testing.T
	svc      *Service
	auth     *coreauth.Auth
	response atomic.Value
	kept     string
	flapping string
}

func newUnlistedCatalogHarness(t *testing.T) *unlistedCatalogHarness {
	t.Helper()
	resetAntigravityCapabilityCache()
	t.Cleanup(resetAntigravityCapabilityCache)
	static := registry.GetAntigravityModels()
	if len(static) < 2 {
		t.Fatal("the static antigravity catalog needs two models")
	}
	h := &unlistedCatalogHarness{t: t, kept: static[0].ID, flapping: static[1].ID}
	h.list(h.kept, h.flapping)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(h.response.Load())
	}))
	t.Cleanup(server.Close)
	manager := coreauth.NewManager(nil, nil, nil)
	h.svc = &Service{cfg: &config.Config{}, coreManager: manager}
	auth, err := manager.Register(t.Context(), antigravityTestAuth("unlisted-"+t.Name(), server.URL))
	if err != nil {
		t.Fatal(err)
	}
	h.auth = auth
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(auth.ID) })
	h.svc.registerModelsForAuth(t.Context(), auth)
	h.svc.WaitAntigravityProbes()
	return h
}

func (h *unlistedCatalogHarness) list(ids ...string) {
	models := make(map[string]any, len(ids))
	for _, id := range ids {
		models[id] = map[string]any{}
	}
	h.response.Store(map[string]any{"models": models})
}

func (h *unlistedCatalogHarness) refresh() {
	h.t.Helper()
	key := h.svc.antigravityCapabilityKey(h.auth)
	antigravityCapabilityMu.Lock()
	entry := antigravityCapabilityCache[key]
	entry.expiresAt = time.Time{}
	antigravityCapabilityCache[key] = entry
	antigravityCapabilityMu.Unlock()
	h.svc.refreshAntigravityModels(h.t.Context())
	h.svc.WaitAntigravityProbes()
}

func (h *unlistedCatalogHarness) registered(id string) bool {
	for _, model := range GlobalModelRegistry().GetModelsForClient(h.auth.ID) {
		if model.ID == id {
			return true
		}
	}
	return false
}

func (h *unlistedCatalogHarness) nextProbeIn() time.Duration {
	antigravityCapabilityMu.RLock()
	defer antigravityCapabilityMu.RUnlock()
	return time.Until(antigravityCapabilityCache[h.svc.antigravityCapabilityKey(h.auth)].expiresAt)
}

func (h *unlistedCatalogHarness) requireRecheckWithinMinutes(when string) {
	h.t.Helper()
	if wait := h.nextProbeIn(); wait <= 0 || wait > 10*time.Minute {
		h.t.Fatalf("%s: next catalog probe in %s, want within minutes", when, wait)
	}
}

func TestAntigravityModelBrieflyUnlistedStaysRegisteredAndIsRecheckedSoon(t *testing.T) {
	h := newUnlistedCatalogHarness(t)
	if !h.registered(h.kept) || !h.registered(h.flapping) || h.nextProbeIn() < time.Hour {
		t.Fatal("a full catalog registers both models on the normal cadence")
	}
	h.list(h.kept)
	for miss := 1; miss < 3; miss++ {
		h.refresh()
		if !h.registered(h.flapping) {
			t.Fatalf("catalog miss %d removed a briefly unlisted model", miss)
		}
		h.requireRecheckWithinMinutes("while the model is unlisted")
	}
	h.refresh()
	if h.registered(h.flapping) {
		t.Fatal("a model missing from three catalogs in a row stayed registered")
	}
	h.requireRecheckWithinMinutes("after removing the unlisted model")
	h.list(h.kept, h.flapping)
	h.refresh()
	if !h.registered(h.flapping) || h.nextProbeIn() < time.Hour {
		t.Fatal("a model the catalog lists again registers and the normal cadence returns")
	}
}

func TestAntigravityModelUnlistedRightAfterRestartStaysRegistered(t *testing.T) {
	h := newUnlistedCatalogHarness(t)
	if _, ok := h.svc.persistedAntigravityHintsForAuth(h.auth, antigravityModelBaseURLs(h.auth)[0]); !ok {
		t.Fatal("a successful catalog is persisted on the auth")
	}
	resetAntigravityCapabilityCache()
	GlobalModelRegistry().UnregisterClient(h.auth.ID)
	h.list(h.kept)
	h.svc.registerModelsForAuth(t.Context(), h.auth)
	h.svc.WaitAntigravityProbes()
	if !h.registered(h.flapping) {
		t.Fatal("the first catalog after a restart removed a model the persisted catalog listed")
	}
	h.requireRecheckWithinMinutes("after the first catalog of a restart")
}

func TestAntigravityEmptyCatalogStillRevokesButIsRecheckedSoon(t *testing.T) {
	h := newUnlistedCatalogHarness(t)
	h.list()
	h.refresh()
	if models := GlobalModelRegistry().GetModelsForClient(h.auth.ID); len(models) != 0 {
		t.Fatalf("an empty catalog revokes every model at once, kept %v", models)
	}
	h.requireRecheckWithinMinutes("after an empty catalog")
	h.list(h.kept, h.flapping)
	h.refresh()
	if !h.registered(h.kept) || !h.registered(h.flapping) {
		t.Fatal("models the catalog lists again register")
	}
}
