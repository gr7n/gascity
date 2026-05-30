package dashboard

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestInjectSupervisorURL verifies the meta-tag placeholder gets
// replaced with the real URL on page load. This is the only dynamic
// bit the Go static server owns.
func TestInjectSupervisorURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		orig string
		want string
	}{
		{
			name: "localhost non-selfclose",
			url:  "http://127.0.0.1:8372",
			orig: `<meta name="supervisor-url" content="">`,
			want: `<meta name="supervisor-url" content="http://127.0.0.1:8372">`,
		},
		{
			name: "vite self-closed form",
			url:  "http://127.0.0.1:8372",
			orig: `<meta name="supervisor-url" content="" />`,
			want: `<meta name="supervisor-url" content="http://127.0.0.1:8372">`,
		},
		{
			name: "html-escape in URL",
			url:  `http://example.com/?q="x"&y=<z>`,
			orig: `<meta name="supervisor-url" content="">`,
			want: `<meta name="supervisor-url" content="http://example.com/?q=&quot;x&quot;&amp;y=&lt;z&gt;">`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(injectSupervisorURL([]byte(tc.orig), tc.url))
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestStaticHandlerServesIndex confirms the handler injects the URL
// into the served index and that dashboard.js is reachable.
func TestStaticHandlerServesIndex(t *testing.T) {
	h, err := NewStaticHandler("http://127.0.0.1:8372")
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}

	// Index.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="supervisor-url" content="http://127.0.0.1:8372">`) {
		t.Errorf("index missing injected supervisor-url meta; body:\n%s", body)
	}

	// Bundle.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard.js: %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("dashboard.js was empty")
	}

	// Unknown path falls back to index.html so the SPA's
	// client-side router (such as it is) can handle unknown
	// routes.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/unknown/deep/path", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback GET: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<meta name="supervisor-url"`) {
		t.Errorf("fallback did not serve SPA index")
	}
}

func TestProxiedHandlerServesRelativeIndexAndForwardsAPI(t *testing.T) {
	var gotPath string
	var gotQuery string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/v0/cities":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"mc-city"}]}`))
		case "/health":
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)

	target, err := url.Parse(api.URL)
	if err != nil {
		t.Fatalf("parse API URL: %v", err)
	}
	h, err := NewProxiedHandler(target)
	if err != nil {
		t.Fatalf("NewProxiedHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<meta name="supervisor-url" content="">`) {
		t.Fatalf("proxied index should keep supervisor URL relative; body:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v0/cities?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v0/cities: %d %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v0/cities" || gotQuery != "limit=10" {
		t.Fatalf("proxied request = %q?%q, want /v0/cities?limit=10", gotPath, gotQuery)
	}
	if !strings.Contains(rec.Body.String(), `"mc-city"`) {
		t.Fatalf("proxied body missing API response: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("GET /health: %d %q", rec.Code, rec.Body.String())
	}
}

func TestDashboardListenAddrUsesLoopback(t *testing.T) {
	if got := dashboardListenAddr(8080); got != "127.0.0.1:8080" {
		t.Fatalf("dashboardListenAddr() = %q, want 127.0.0.1:8080", got)
	}
}

func TestStaticHandlerAcceptsClientLogs(t *testing.T) {
	h, err := NewStaticHandler("http://127.0.0.1:8372")
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}

	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/__client-log", strings.NewReader(`{
		"ts":"2026-04-17T16:00:00Z",
		"level":"error",
		"scope":"mail",
		"message":"Compose failed",
		"details":{"reason":"missing recipient"},
		"url":"http://localhost:8080/?city=mc-city",
		"city":"mc-city"
	}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /__client-log: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `client[error]`) {
		t.Fatalf("client log output missing level: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `scope=mail`) {
		t.Fatalf("client log output missing scope: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"reason":"missing recipient"`) {
		t.Fatalf("client log output missing details: %s", logs.String())
	}
}

func TestStaticHandlerAcceptsClientLogBatches(t *testing.T) {
	h, err := NewStaticHandler("http://127.0.0.1:8372")
	if err != nil {
		t.Fatalf("NewStaticHandler: %v", err)
	}

	var logs bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/__client-log", strings.NewReader(`[
		{"level":"warn","scope":"sse","message":"refresh delayed","details":{"pending":2}},
		{"level":"error","scope":"api","message":"request failed","details":{"status":500}}
	]`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /__client-log: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `client[warn]`) || !strings.Contains(logs.String(), `scope=sse`) {
		t.Fatalf("client log batch missing warn entry: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `client[error]`) || !strings.Contains(logs.String(), `scope=api`) {
		t.Fatalf("client log batch missing error entry: %s", logs.String())
	}
}
