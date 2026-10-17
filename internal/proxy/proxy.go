// Package proxy drives the kayly-proxy container (Caddy). The daemon generates the full
// Caddy config from its database and loads it through Caddy's admin API, which applies it
// atomically, keeps the old config on failure and ignores a load that changes nothing.
package proxy

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"time"
)

// Route sends HTTPS traffic for Host to Upstream, a "host:port" on the edge network.
type Route struct {
	Host     string
	Upstream string
}

// Options are the parts of the Caddy config that do not depend on routes.
type Options struct {
	// AdminListen is the admin API address inside the proxy container, e.g.
	// "unix//run/kayly/caddy.sock". It must match the address the daemon dials, or Caddy
	// would move its admin endpoint away from the daemon on the first load.
	AdminListen string
	// Email is given to the ACME CA for expiry notices. Optional.
	Email string
}

// Config returns the Caddy JSON config for routes. The output is deterministic, so equal
// inputs produce byte-equal configs.
func Config(routes []Route, opt Options) ([]byte, error) {
	if opt.AdminListen == "" {
		return nil, fmt.Errorf("proxy: admin listen address is required")
	}
	routes = slices.Clone(routes)
	slices.SortFunc(routes, func(a, b Route) int { return cmp.Compare(a.Host, b.Host) })

	caddyRoutes := make([]route, 0, len(routes))
	for i, r := range routes {
		if r.Host == "" || r.Upstream == "" {
			return nil, fmt.Errorf("proxy: route %+v: host and upstream are required", r)
		}
		if _, _, err := net.SplitHostPort(r.Upstream); err != nil {
			return nil, fmt.Errorf("proxy: route %s: upstream: %w", r.Host, err)
		}
		if i > 0 && routes[i-1].Host == r.Host {
			return nil, fmt.Errorf("proxy: host %s routed twice", r.Host)
		}
		caddyRoutes = append(caddyRoutes, route{
			Match: []match{{Host: []string{r.Host}}},
			Handle: []handler{
				{
					Handler:   "encode",
					Encodings: map[string]struct{}{"gzip": {}, "zstd": {}},
					Prefer:    []string{"zstd", "gzip"},
				},
				{
					Handler:   "reverse_proxy",
					Upstreams: []upstream{{Dial: r.Upstream}},
					// Flush every write: SSE and streamed responses must not wait in a buffer.
					FlushInterval: -1,
				},
			},
			Terminal: true,
		})
	}

	c := config{Admin: admin{Listen: opt.AdminListen}}
	c.Apps.HTTP.Servers = map[string]server{"kayly": {Listen: []string{":443"}, Routes: caddyRoutes}}
	if opt.Email != "" {
		c.Apps.TLS = &tlsApp{Automation: automation{Policies: []policy{{
			Issuers: []issuer{{Module: "acme", Email: opt.Email}},
		}}}}
	}
	return json.MarshalIndent(c, "", "  ")
}

// The subset of Caddy's JSON schema Kayly uses: https://caddyserver.com/docs/json/
type (
	config struct {
		Admin admin `json:"admin"`
		Apps  struct {
			HTTP struct {
				Servers map[string]server `json:"servers"`
			} `json:"http"`
			TLS *tlsApp `json:"tls,omitempty"`
		} `json:"apps"`
	}
	admin  struct{ Listen string `json:"listen"` }
	server struct {
		Listen []string `json:"listen"`
		Routes []route  `json:"routes"`
	}
	route struct {
		Match    []match   `json:"match"`
		Handle   []handler `json:"handle"`
		Terminal bool      `json:"terminal"`
	}
	match   struct{ Host []string `json:"host"` }
	handler struct {
		Handler       string              `json:"handler"`
		Encodings     map[string]struct{} `json:"encodings,omitempty"`
		Prefer        []string            `json:"prefer,omitempty"`
		Upstreams     []upstream          `json:"upstreams,omitempty"`
		FlushInterval int                 `json:"flush_interval,omitempty"`
	}
	upstream   struct{ Dial string `json:"dial"` }
	tlsApp     struct{ Automation automation `json:"automation"` }
	automation struct{ Policies []policy `json:"policies"` }
	policy     struct{ Issuers []issuer `json:"issuers"` }
	issuer     struct {
		Module string `json:"module"`
		Email  string `json:"email,omitempty"`
	}
)

// Client talks to Caddy's admin API over a unix socket.
type Client struct {
	http *http.Client
}

// NewClient returns a client for the admin socket at path.
func NewClient(socket string) *Client {
	return &Client{http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

// Load replaces Caddy's config. Caddy applies it with zero downtime, keeps the previous
// config if the new one fails, and does nothing if the config is unchanged.
func (c *Client) Load(ctx context.Context, cfg []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://caddy/load", bytes.NewReader(cfg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxy: load config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("proxy: load config: %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	return nil
}
