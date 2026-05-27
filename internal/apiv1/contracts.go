package apiv1

const Version = "v1"

type Envelope struct {
	APIVersion string `json:"api_version"`
	OK         bool   `json:"ok"`
	Data       any    `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type DNSSettings struct {
	Mode           string   `json:"mode"`
	Servers        []string `json:"servers,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type HotspotSettings struct {
	Enabled    *bool    `json:"enabled,omitempty"`
	TCP        *bool    `json:"tcp,omitempty"`
	DNS        *bool    `json:"dns,omitempty"`
	Interfaces []string `json:"interfaces,omitempty"`
}

// LeakProtectionSettings is the patch surface for v1.1.0 leak protection
// toggles. All fields are pointer-bool so the caller can change one
// toggle without resetting the others — matching the existing pattern in
// HotspotSettings.
type LeakProtectionSettings struct {
	BlockIPv6Leaks *bool `json:"block_ipv6_leaks,omitempty"`
	BlockQUIC      *bool `json:"block_quic,omitempty"`
	FlushConntrack *bool `json:"flush_conntrack,omitempty"`
	RouteLocalnet  *bool `json:"route_localnet,omitempty"`
}

type ConfigPatchRequest struct {
	DNS             *DNSSettings            `json:"dns,omitempty"`
	Hotspot         *HotspotSettings        `json:"hotspot,omitempty"`
	LeakProtection  *LeakProtectionSettings `json:"leak_protection,omitempty"`
	Restart         bool                    `json:"restart,omitempty"`
}

type ConfigUpdateResponse struct {
	Config         any      `json:"config"`
	Restart        bool     `json:"restart"`
	RestartPending bool     `json:"restart_pending"`
	Changed        []string `json:"changed"`
}

type PublicIPResponse struct {
	CheckedAt string           `json:"checked_at"`
	Provider  string           `json:"provider"`
	Device    *PublicIPDetails `json:"device,omitempty"`
	Tunnel    *PublicIPDetails `json:"tunnel,omitempty"`
}

type PublicIPDetails struct {
	Path      string `json:"path"`
	OK        bool   `json:"ok"`
	IP        string `json:"ip,omitempty"`
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
	City      string `json:"city,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Org       string `json:"org,omitempty"`
	ASN       string `json:"asn,omitempty"`
	ASName    string `json:"as_name,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	Cached    bool   `json:"cached"`
}

type DiagnosticsResponse struct {
	Runtime     any            `json:"runtime"`
	Config      any            `json:"config"`
	Pool        any            `json:"pool"`
	Route       any            `json:"route"`
	Performance map[string]any `json:"performance"`
	Logs        map[string]any `json:"logs"`
}

type Capability struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
}
