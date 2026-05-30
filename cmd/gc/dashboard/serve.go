package dashboard

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Serve starts the dashboard HTTP server. The dashboard is a static
// TypeScript SPA that calls the supervisor's typed OpenAPI endpoints
// directly from the browser. This function embeds + serves the compiled
// bundle and injects `supervisorURL` into the page so the SPA knows where
// to reach the supervisor.
func Serve(port int, supervisorURL string) error {
	supervisorURL = strings.TrimRight(strings.TrimSpace(supervisorURL), "/")
	if supervisorURL == "" {
		return fmt.Errorf("dashboard: supervisor URL is empty; pass --api")
	}

	handler, err := NewStaticHandler(supervisorURL)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("dashboard: listening on http://localhost%s (supervisor=%s)", addr, supervisorURL)
	return http.ListenAndServe(addr, logRequest(handler))
}

// ServeProxied starts a same-origin dashboard server. The browser talks to
// this dashboard origin for /v0/ and /health, while the dashboard server
// forwards those requests to supervisorURL. This mode is useful behind HTTPS
// tunnels where the browser cannot reach a private supervisor address.
func ServeProxied(port int, supervisorURL string) error {
	supervisorURL = strings.TrimRight(strings.TrimSpace(supervisorURL), "/")
	if supervisorURL == "" {
		return fmt.Errorf("dashboard: supervisor URL is empty; pass --api")
	}
	parsedSupervisorURL, err := url.Parse(supervisorURL)
	if err != nil || parsedSupervisorURL.Scheme == "" || parsedSupervisorURL.Host == "" {
		return fmt.Errorf("dashboard: invalid supervisor URL %q", supervisorURL)
	}
	handler, err := NewProxiedHandler(parsedSupervisorURL)
	if err != nil {
		return err
	}
	addr := dashboardListenAddr(port)
	log.Printf("dashboard: listening on http://%s (supervisor=%s)", addr, supervisorURL)
	return http.ListenAndServe(addr, logRequest(handler))
}

func dashboardListenAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}
