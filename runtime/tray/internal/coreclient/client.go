// Package coreclient talks to the Core loopback admin API.
// It does not start a Core and it does not log response bodies.
package coreclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultAdminURL   = "http://127.0.0.1:7742"
	PathUI            = "/api/core/ui"
	PathPause         = "/api/mesh/pause"
	PathResume        = "/api/mesh/resume"
	PathStop          = "/api/core/stop"
	PathPeers         = "/api/peers"
	PathConversations = "/api/chat/conversations"
	maxBody           = 64 << 10
)

var (
	ErrUnreachable = errors.New("core unreachable")
	ErrNotLoopback = errors.New("core url is not loopback")
	ErrBadStatus   = errors.New("core returned an unreadable status")
)

// StatusError is an HTTP failure. The body is discarded so secrets are not copied into logs.
type StatusError struct {
	Path string
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("core %s returned %d", e.Path, e.Code)
}

// Status is the loopback admin view returned by the Core.
type Status struct {
	URL     string `json:"url"`
	Mesh    string `json:"mesh"`
	Sync    bool   `json:"sync"`
	Stopped bool   `json:"stopped"`
}

// Link is one peer sync arc from GET /api/peers.
type Link struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// Peers is the part of GET /api/peers the tray can read without guessing.
type Peers struct {
	CountKnown    bool
	Count         int
	MeshIDs       []string
	Links         []Link
	Handoffs      []string
	RingingCallID        string
	TerminalActiveCount  int
	TerminalInviteID     string
	TerminalControlID    string
}

// Client calls the admin API on a loopback base URL.
type Client struct {
	base string
	http *http.Client
}

// DefaultBaseURL is OVERDRIVE_TRAY_CORE_URL, otherwise OVERDRIVE_SHARE_UI when it
// is an address, otherwise http://127.0.0.1:7742.
func DefaultBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_TRAY_CORE_URL")); v != "" {
		return v
	}
	v := strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_UI"))
	switch v {
	case "", "off", "0":
		return DefaultAdminURL
	}
	if strings.Contains(v, "://") {
		return v
	}
	return "http://" + v
}

// New rejects any base URL that is not http(s) on a loopback host.
func New(base string) (*Client, error) {
	if strings.TrimSpace(base) == "" {
		base = DefaultBaseURL()
	}
	u, err := ParseLoopback(base)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return &Client{base: strings.TrimRight(u.String(), "/"), http: defaultHTTP()}, nil
}

// Base is the loopback admin origin, without a trailing slash.
func (c *Client) Base() string {
	if c == nil {
		return ""
	}
	return c.base
}

func defaultHTTP() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirect")
		},
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) { return nil, nil },
		},
	}
}

// ParseLoopback accepts http or https on a loopback host and rejects userinfo.
func ParseLoopback(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, ErrNotLoopback
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrNotLoopback
	}
	if u.User != nil {
		return nil, ErrNotLoopback
	}
	if !loopbackHost(u.Hostname()) {
		return nil, ErrNotLoopback
	}
	return u, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateUIURL returns a loopback UI URL safe to hand to the OS browser.
func ValidateUIURL(raw string) (string, error) {
	u, err := ParseLoopback(raw)
	if err != nil {
		return "", err
	}
	u.User = nil
	u.Fragment = ""
	return u.String(), nil
}

func (c *Client) UI(ctx context.Context) (Status, error) {
	return c.roundTrip(ctx, http.MethodGet, PathUI)
}

func (c *Client) Pause(ctx context.Context) (Status, error) {
	return c.roundTrip(ctx, http.MethodPost, PathPause)
}

func (c *Client) Resume(ctx context.Context) (Status, error) {
	return c.roundTrip(ctx, http.MethodPost, PathResume)
}

func (c *Client) Stop(ctx context.Context) (Status, error) {
	return c.roundTrip(ctx, http.MethodPost, PathStop)
}

func (c *Client) roundTrip(ctx context.Context, method, path string) (Status, error) {
	body, err := c.call(ctx, method, path)
	if err != nil {
		return Status{}, err
	}
	if len(body) == 0 && path == PathStop {
		return Status{Stopped: true}, nil
	}
	var st Status
	if json.Unmarshal(body, &st) != nil {
		return Status{}, ErrBadStatus
	}
	return st, nil
}

// Peers reads GET /api/peers. CountKnown is false when the payload has no circles array.
func (c *Client) Peers(ctx context.Context) (Peers, error) {
	body, err := c.call(ctx, http.MethodGet, PathPeers)
	if err != nil {
		return Peers{}, err
	}
	return decodePeers(body)
}

// Unread sums conversation unread counts from GET /api/chat/conversations.
// meshIDs come from circle ids on GET /api/peers. A failed call or a missing
// unread field leaves the count unknown so the menu does not invent a number.
func (c *Client) Unread(ctx context.Context, meshIDs []string) (count int, known bool) {
	ids := uniqueIDs(meshIDs)
	if len(ids) == 0 {
		return 0, false
	}
	total := 0
	for _, id := range ids {
		body, err := c.call(ctx, http.MethodGet, conversationsPath(id))
		if err != nil {
			return 0, false
		}
		n, ok := sumConversationUnread(body)
		if !ok {
			return 0, false
		}
		total += n
	}
	return total, true
}

func conversationsPath(meshID string) string {
	q := url.Values{}
	q.Set("mesh_id", meshID)
	return PathConversations + "?" + q.Encode()
}

func uniqueIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (c *Client) call(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, ErrUnreachable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "overdrive-tray")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, ErrUnreachable
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, ErrUnreachable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Path: path, Code: resp.StatusCode}
	}
	return payload, nil
}

func (c *Client) httpClient() *http.Client {
	if c.http != nil {
		return c.http
	}
	return defaultHTTP()
}

func decodePeers(body []byte) (Peers, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return Peers{}, ErrBadStatus
	}
	var out Peers
	if circles, ok := raw["circles"]; ok {
		out.CountKnown = true
		n, err := countPeers(raw["device_id"], circles)
		if err != nil {
			return Peers{}, ErrBadStatus
		}
		out.Count = n
		ids, err := circleMeshIDs(circles)
		if err != nil {
			return Peers{}, ErrBadStatus
		}
		out.MeshIDs = ids
	}
	if links, ok := raw["links"]; ok {
		if json.Unmarshal(links, &out.Links) != nil {
			return Peers{}, ErrBadStatus
		}
	}
	if hands, ok := raw["handoffs"]; ok && string(hands) != "null" {
		if json.Unmarshal(hands, &out.Handoffs) != nil {
			return Peers{}, ErrBadStatus
		}
	}
	decodePeersTerminalFields(raw, &out)
	return out, nil
}

func decodePeersTerminalFields(raw map[string]json.RawMessage, out *Peers) {
	if inc, ok := raw["incoming_call"]; ok && string(inc) != "null" {
		var ring struct {
			CallID string `json:"call_id"`
		}
		if json.Unmarshal(inc, &ring) == nil {
			out.RingingCallID = strings.TrimSpace(ring.CallID)
		}
	}
	if v, ok := raw["terminal_active_count"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil {
			out.TerminalActiveCount = n
		}
	}
	if v, ok := raw["terminal_invite_id"]; ok {
		_ = json.Unmarshal(v, &out.TerminalInviteID)
		out.TerminalInviteID = strings.TrimSpace(out.TerminalInviteID)
	}
	if v, ok := raw["terminal_control_id"]; ok {
		_ = json.Unmarshal(v, &out.TerminalControlID)
		out.TerminalControlID = strings.TrimSpace(out.TerminalControlID)
	}
}

// CallDeepLink returns the dashboard URL focused on the call panel.
func CallDeepLink(base string) string {
	u, err := ValidateUIURL(base)
	if err != nil {
		return base
	}
	return strings.TrimRight(u, "/") + "/#call"
}

// SessionsDeepLink returns the dashboard URL focused on Live Sessions.
func SessionsDeepLink(base string) string {
	u, err := ValidateUIURL(base)
	if err != nil {
		return base
	}
	return strings.TrimRight(u, "/") + "/#live-sessions"
}

func countPeers(deviceRaw, circlesRaw json.RawMessage) (int, error) {
	var self string
	if len(deviceRaw) > 0 {
		_ = json.Unmarshal(deviceRaw, &self)
	}
	var circles []struct {
		Members []struct {
			DeviceID string `json:"device_id"`
			Self     bool   `json:"self"`
			Revoked  bool   `json:"revoked"`
		} `json:"members"`
		PeerEndpoints []string `json:"peer_endpoints"`
	}
	if err := json.Unmarshal(circlesRaw, &circles); err != nil {
		return 0, err
	}
	seen := map[string]struct{}{}
	usedMembers := false
	for _, circle := range circles {
		if circle.Members != nil {
			usedMembers = true
			for _, member := range circle.Members {
				id := strings.TrimSpace(member.DeviceID)
				if member.Revoked || member.Self || id == "" || id == self {
					continue
				}
				seen[id] = struct{}{}
			}
		}
	}
	if usedMembers {
		return len(seen), nil
	}
	endpoints := map[string]struct{}{}
	for _, circle := range circles {
		for _, ep := range circle.PeerEndpoints {
			ep = strings.TrimSpace(ep)
			if ep != "" {
				endpoints[ep] = struct{}{}
			}
		}
	}
	return len(endpoints), nil
}

func circleMeshIDs(circlesRaw json.RawMessage) ([]string, error) {
	var circles []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(circlesRaw, &circles); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(circles))
	for _, circle := range circles {
		ids = append(ids, circle.ID)
	}
	return uniqueIDs(ids), nil
}

// sumConversationUnread totals unread across conversations. The preview text is ignored.
// An empty list is a real zero. A missing unread field makes the payload unusable.
func sumConversationUnread(body []byte) (int, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return 0, false
	}
	field, ok := raw["conversations"]
	if !ok {
		return 0, false
	}
	var conversations []map[string]json.RawMessage
	if json.Unmarshal(field, &conversations) != nil {
		return 0, false
	}
	total := 0
	for _, conversation := range conversations {
		unread, ok := conversation["unread"]
		if !ok {
			return 0, false
		}
		var n int
		if json.Unmarshal(unread, &n) != nil || n < 0 {
			return 0, false
		}
		total += n
	}
	return total, true
}
