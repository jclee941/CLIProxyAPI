package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestFlowBootstrapCompletesPassiveGoogleLogin(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "fixture")
	var targets []string
	var mu sync.Mutex
	login := "https://accounts.google.com/ServiceLogin?passive=1209600&continue=https%3A%2F%2Fflow.google.com%2Fprojects"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var exchange struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(request.Body).Decode(&exchange); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		targets = append(targets, exchange.URL)
		mu.Unlock()
		response := exchangeResult{Headers: http.Header{}}
		switch exchange.URL {
		case flowOrigin + "/projects":
			response.StatusCode = 302
			response.Headers.Set("Location", login)
		case login:
			response.StatusCode = 302
			response.Headers.Set("Location", flowOrigin+"/projects?passiveDone=1")
		case flowOrigin + "/projects?passiveDone=1":
			response.StatusCode = 200
			response.Body = []byte(`<script>{"FdrFJe":"42","SNlM0e":"xsrf"}</script>boq_labs-ai-sandbox-frontend_fixture`)
		default:
			t.Errorf("unexpected target %q", exchange.URL)
			response.StatusCode = 404
		}
		writeFixture(t, writer, string(jsonFixture(t, response)))
	}))
	defer server.Close()
	service := newService(nil)
	service.config.SessionBrokerURL = server.URL
	session := service.newFlowSession("gemini-web-a.json")

	err := session.bootstrap(t.Context(), "/projects")

	mu.Lock()
	defer mu.Unlock()
	if err != nil || session.page.build == "" || len(targets) != 3 {
		t.Fatalf("passive login failed: %v, requests=%v", err, targets)
	}
}
