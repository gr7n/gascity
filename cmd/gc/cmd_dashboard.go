package main

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/cmd/gc/dashboard"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/spf13/cobra"
)

var (
	dashboardServeHook          = dashboard.Serve
	dashboardServeProxiedHook   = dashboard.ServeProxied
	dashboardServeProxiedOnHook = dashboard.ServeProxiedOn
)

type dashboardServeOptions struct {
	bind           string
	proxyAPIRead   bool
	proxyAPIMutate bool
}

// newDashboardCmd creates the "gc dashboard" command group.
func newDashboardCmd(stdout, stderr io.Writer) *cobra.Command {
	var port int
	var apiURL string
	var bind string
	var proxyAPIRead bool
	var proxyAPIMutate bool
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Web dashboard for monitoring the supervisor and managed cities",
		Long: `Open the static GC dashboard against the machine-wide supervisor API.

Without a city in scope, the dashboard shows supervisor-level state and managed
city tabs. From a city directory or with --city, city-specific panels and action
forms are enabled for that city.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			opts := dashboardServeOptions{
				bind:           bind,
				proxyAPIRead:   proxyAPIRead,
				proxyAPIMutate: proxyAPIMutate,
			}
			if runDashboardServeWithOptions("gc dashboard", port, apiURL, stderr, opts) != nil {
				return errExit
			}
			return nil
		},
	}
	bindDashboardServeFlags(cmd, &port, &apiURL, &bind, &proxyAPIRead, &proxyAPIMutate)
	cmd.AddCommand(newDashboardServeCmd(stdout, stderr))
	return cmd
}

// newDashboardServeCmd creates the "gc dashboard serve" subcommand.
func newDashboardServeCmd(_, stderr io.Writer) *cobra.Command {
	var port int
	var apiURL string
	var bind string
	var proxyAPIRead bool
	var proxyAPIMutate bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web dashboard",
		Long: `Start the static GC dashboard against the machine-wide supervisor API.

Without a city in scope, the dashboard shows supervisor-level state and managed
city tabs. From a city directory or with --city, city-specific panels and action
forms are enabled for that city.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			opts := dashboardServeOptions{
				bind:           bind,
				proxyAPIRead:   proxyAPIRead,
				proxyAPIMutate: proxyAPIMutate,
			}
			if runDashboardServeWithOptions("gc dashboard serve", port, apiURL, stderr, opts) != nil {
				return errExit
			}
			return nil
		},
	}
	bindDashboardServeFlags(cmd, &port, &apiURL, &bind, &proxyAPIRead, &proxyAPIMutate)
	return cmd
}

func bindDashboardServeFlags(cmd *cobra.Command, port *int, apiURL *string, bind *string, proxyAPIRead *bool, proxyAPIMutate *bool) {
	cmd.Flags().IntVar(port, "port", 8080, "HTTP port")
	cmd.Flags().StringVar(apiURL, "api", "", "GC API server URL override (auto-discovered by default)")
	cmd.Flags().StringVar(bind, "bind", "", "HTTP bind address override for the dashboard listener")
	cmd.Flags().BoolVar(proxyAPIRead, "proxy-api-read", false, "proxy read-only supervisor API requests through the dashboard origin")
	cmd.Flags().BoolVar(proxyAPIMutate, "proxy-api-mutate", false, "proxy mutating supervisor API requests through the dashboard origin; require an external access-control layer")
}

func runDashboardServe(commandName string, port int, apiURLOverride string, stderr io.Writer) error {
	return runDashboardServeWithOptions(commandName, port, apiURLOverride, stderr, dashboardServeOptions{})
}

func runDashboardServeWithOptions(commandName string, port int, apiURLOverride string, stderr io.Writer, opts dashboardServeOptions) error {
	cityPath, cfg, err := resolveDashboardContext(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", commandName, err) //nolint:errcheck // best-effort stderr
		return err
	}

	apiURL, err := resolveDashboardAPI(cityPath, cfg, apiURLOverride)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", commandName, err) //nolint:errcheck // best-effort stderr
		return err
	}

	if opts.proxyAPIRead || opts.proxyAPIMutate {
		proxyOptions := dashboard.ProxyOptions{AllowMutations: opts.proxyAPIMutate}
		if proxyOptions.AllowMutations {
			fmt.Fprintln(stderr, "warning: mutating dashboard API proxy enabled; only use behind a trusted access-control layer such as Cloudflare Access, Tailscale, VPN, or SSH") //nolint:errcheck // best-effort stderr
		}
		if bind := strings.TrimSpace(opts.bind); bind != "" {
			if err := dashboardServeProxiedOnHook(bind, port, apiURL, proxyOptions); err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", commandName, err) //nolint:errcheck // best-effort stderr
				return err
			}
			return nil
		}
		if err := dashboardServeProxiedHook(port, apiURL, proxyOptions); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", commandName, err) //nolint:errcheck // best-effort stderr
			return err
		}
		return nil
	}
	if err := dashboardServeHook(port, apiURL); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", commandName, err) //nolint:errcheck // best-effort stderr
		return err
	}
	return nil
}

func resolveDashboardContext(warningWriter ...io.Writer) (cityPath string, cfg *config.City, err error) {
	cityPath, err = resolveCity()
	if err != nil {
		if strings.TrimSpace(cityFlag) == "" && strings.Contains(err.Error(), "not in a city directory") {
			return "", nil, nil
		}
		return "", nil, err
	}
	cfg, err = loadCityConfig(cityPath, warningWriter...)
	if err != nil {
		return "", nil, err
	}
	return cityPath, cfg, nil
}

func resolveDashboardAPI(cityPath string, cfg *config.City, apiURLOverride string) (apiURL string, err error) {
	if override := strings.TrimSpace(apiURLOverride); override != "" {
		return strings.TrimRight(override, "/"), nil
	}

	if supervisorAliveHook() != 0 {
		baseURL, err := supervisorAPIBaseURL()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(baseURL, "/"), nil
	}

	if cityPath == "" {
		return "", fmt.Errorf("could not auto-discover the supervisor API; start the supervisor with %q or pass --api explicitly", "gc supervisor start")
	}
	// Standalone-controller mode: the controller's API (cfg.API.Port)
	// now serves the same /v0/city/{cityName}/... surface as the
	// supervisor via api.NewSupervisorMux, so it is a valid target
	// for `gc dashboard`. Return the local address when the config
	// declares a listening port; the dashboard will call ListCities
	// to discover which city/cities are served.
	if hasStandaloneDashboardAPI(cfg) {
		return standaloneAPIBaseURL(cfg), nil
	}
	return "", fmt.Errorf("could not auto-discover the supervisor API for %q; start the supervisor with %q or pass --api explicitly", cityPath, "gc supervisor start")
}

func hasStandaloneDashboardAPI(cfg *config.City) bool {
	return cfg != nil && cfg.API.Port > 0
}

// standaloneAPIBaseURL assembles the local URL of the controller's API.
// The controller publishes /v0/city/{cityName}/... routes, so the CLI
// can target it the same way it targets the supervisor.
//
// Bind normalization:
//   - "" → 127.0.0.1 (empty = default in config.API.BindOrDefault edge cases)
//   - "0.0.0.0" → 127.0.0.1 (listener accepts any v4; connect to loopback)
//   - "::" → ::1 (listener accepts any v6; connect to loopback)
//
// Non-wildcard binds (explicit 127.0.0.1, ::1, 192.168.x.x, 2001::...) are
// passed through unchanged. net.JoinHostPort wraps IPv6 literals in
// brackets so the URL parser sees `http://[::1]:8080/...` correctly;
// plain fmt.Sprintf would produce `http://::1:8080` which parses as
// host=":" port="1:8080" and fails.
func standaloneAPIBaseURL(cfg *config.City) string {
	bind := cfg.API.BindOrDefault()
	switch bind {
	case "", "0.0.0.0":
		bind = "127.0.0.1"
	case "::", "[::]":
		bind = "::1"
	}
	return "http://" + net.JoinHostPort(bind, strconv.Itoa(cfg.API.Port))
}
