// webapi.go — Extended HTTP API endpoints for the v1.0.1 WebUI.
// Adds CORS, enriched /status, profiles, vpnchain, logs, diagnostics,
// latency, events (SSE), and config-patch on top of v2.3.13's daemon.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	issh "github.com/GoodyOG/SSHCustom_Magisk/internal/ssh"
)


// ── CORS middleware ───────────────────────────────────────────────────────────
// Required so the KSU-manager WebView (different origin) can call the API.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ── Enriched status shape the v1.0.1 WebUI expects ───────────────────────────
// The v2.3.13 snapshot has different field names; we translate here so the
// existing WebUI JS works without changes.
func buildEnrichedStatus(snap interface{}, cfg interface{}, workDir string, socks_port, redir_port int) map[string]interface{} {
	// snap is api.StatusSnapshot — use reflection-free type assertion via JSON round-trip
	b, _ := json.Marshal(snap)
	var s map[string]interface{}
	json.Unmarshal(b, &s)

	connected := s["connected"] == true
	uptimeSec, _ := s["uptime_seconds"].(float64)
	memMB, _ := s["mem_rss_mb"].(float64)
	cpu, _ := s["cpu_percent"].(float64)
	upKbps, _ := s["up_kbps"].(float64)
	downKbps, _ := s["down_kbps"].(float64)
	activeConns, _ := s["active_connections"].(float64)
	ver, _ := s["version"].(string)
	lastErr, _ := s["last_error"].(string)
	sshMode, _ := s["ssh_mode"].(string)
	netMode, _ := s["network_mode"].(string)

	// Pool info from config (v2.3.13 stores pool size in config)
	var poolSize float64 = 8
	if cfg != nil {
		b2, _ := json.Marshal(cfg)
		var c2 map[string]interface{}
		json.Unmarshal(b2, &c2)
		if ps, ok := c2["channel_pool_size"].(float64); ok && ps > 0 {
			poolSize = ps
		}
	}


	// Derive network route info from system (best effort)
	sourceIP, iface, gateway := getDefaultRoute()
	networkOnline := sourceIP != ""

	state := "disconnected"
	if connected {
		state = "connected"
	}

	runtime := map[string]interface{}{
		// v1.0.1 WebUI primary fields
		"connected":           connected,
		"ssh_authenticated":   connected, // v1.0.1 uses this to gate "Tunnel Connected"
		"running":             true,
		"daemon_online":       true,
		"state":               state,
		"tunnel_uptime_seconds": int64(uptimeSec),
		"uptime_seconds":      int64(uptimeSec),
		"memory_rss_bytes":    int64(memMB * 1024 * 1024),
		"mem_rss_mb":          memMB,
		"cpu_percent":         cpu,
		"up_kbps":             upKbps,
		"down_kbps":           downKbps,
		"active_connections":  int(activeConns),
		"version":             ver,
		"last_error":          lastErr,
		"last_event":          state,
		"selected_mode":       sshMode,
		"transport_chain":     netMode,
		// Pool — v2.3.13 uses fixed pool; show size as both healthy and size
		"pool_healthy":        int(poolSize),
		"pool_size":           int(poolSize),
		"pool_streams":        int(activeConns),
		"pool_max_streams":    64,
		// Network route
		"source_ip":          sourceIP,
		"interface":          iface,
		"gateway":            gateway,
		"network_online":     networkOnline,
		// Proxy listeners
		"socks_running":       connected,
		"socks_addr":          fmt.Sprintf("127.0.0.1:%d", socks_port),
		"transparent_running": connected,
		"transparent_addr":    fmt.Sprintf("0.0.0.0:%d", redir_port),
		// Speed
		"bytes_sent": s["bytes_sent"],
		"bytes_recv": s["bytes_recv"],
		// Resolver info
		"dns_mode":       "device",
		"resolver_method": "android_shell_dns",
		"resolved_ips":   []string{},
	}

	return runtime
}


// getDefaultRoute returns (sourceIP, interface, gateway) by reading /proc/net/route
func getDefaultRoute() (srcIP, iface, gw string) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", "", ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		if fields[1] == "00000000" { // destination 0.0.0.0 = default route
			iface = fields[0]
			// Gateway is hex little-endian
			gwHex := fields[2]
			if len(gwHex) == 8 {
				b := make([]byte, 4)
				for i := 0; i < 4; i++ {
					v, _ := strconv.ParseUint(gwHex[i*2:i*2+2], 16, 8)
					b[3-i] = byte(v)
				}
				gw = fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
			}
			break
		}
	}
	if iface == "" {
		return "", "", ""
	}
	// Get source IP on that interface
	addrs, _ := net.InterfaceAddrs()
	ifaceObj, _ := net.InterfaceByName(iface)
	if ifaceObj != nil {
		ifAddrs, _ := ifaceObj.Addrs()
		for _, a := range ifAddrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				srcIP = ipnet.IP.String()
				break
			}
		}
	}
	_ = addrs
	return srcIP, iface, gw
}


// ── Profiles store ────────────────────────────────────────────────────────────
type profilesStore struct {
	mu   sync.Mutex
	path string
}

func newProfilesStore(workDir string) *profilesStore {
	return &profilesStore{path: filepath.Join(workDir, "profiles.json")}
}

func (ps *profilesStore) load() (map[string]interface{}, error) {
	data, err := os.ReadFile(ps.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{"selected_id": "", "profiles": []interface{}{}}, nil
		}
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out["profiles"] == nil {
		out["profiles"] = []interface{}{}
	}
	return out, nil
}

func (ps *profilesStore) save(data map[string]interface{}) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ps.path, b, 0600)
}


// applyProfileToSettings writes a profile's SSH fields into settings.ini
func applyProfileToSettings(p map[string]interface{}, cfgPath string) error {
	ssh, _ := p["ssh"].(map[string]interface{})
	transport, _ := p["transport"].(map[string]interface{})
	if ssh == nil {
		return nil
	}
	host, _ := ssh["host"].(string)
	port := 22
	if pf, ok := ssh["port"].(float64); ok {
		port = int(pf)
	}
	user, _ := ssh["username"].(string)
	pass, _ := ssh["password"].(string)
	mode := "direct"
	sni := ""
	proxyHost := ""
	proxyPort := 3128
	payloadEnabled := false
	payload := ""
	if transport != nil {
		if m, ok := transport["mode"].(string); ok {
			mode = m
		}
		if tls, ok := transport["tls"].(map[string]interface{}); ok {
			sni, _ = tls["server_name"].(string)
		}
		if hp, ok := transport["http_proxy"].(map[string]interface{}); ok {
			proxyHost, _ = hp["host"].(string)
			if pp, ok := hp["port"].(float64); ok {
				proxyPort = int(pp)
			}
		}
		if pl, ok := transport["payload"].(map[string]interface{}); ok {
			payloadEnabled, _ = pl["enabled"].(bool)
			payload, _ = pl["template"].(string)
		}
	}
	// Map mode names: v1.0.1 → v2.3.13
	modeMap := map[string]string{
		"direct":             "direct",
		"http_proxy":         "sni_http_proxy",
		"tls_sni":            "sni",
		"http_proxy_tls_sni": "sni_http_proxy",
	}
	if m, ok := modeMap[mode]; ok {
		mode = m
	}
	pairs := map[string]string{
		"ssh_host":        host,
		"ssh_port":        strconv.Itoa(port),
		"ssh_user":        user,
		"ssh_password":    pass,
		"ssh_mode":        mode,
		"ssh_sni_host":    sni,
		"http_proxy_host": proxyHost,
		"http_proxy_port": strconv.Itoa(proxyPort),
		"payload_enabled": strconv.FormatBool(payloadEnabled),
		"payload":         payload,
	}
	return patchSettingsINI(cfgPath, pairs)
}


// patchSettingsINI writes key="value" pairs into settings.ini in-place
func patchSettingsINI(path string, pairs map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		eq := strings.IndexByte(t, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(t[:eq])
		if val, ok := pairs[key]; ok {
			esc := strings.ReplaceAll(val, `"`, `\"`)
			lines[i] = key + `="` + esc + `"`
			seen[key] = true
		}
	}
	for k, v := range pairs {
		if !seen[k] {
			esc := strings.ReplaceAll(v, `"`, `\"`)
			lines = append(lines, k+`="`+esc+`"`)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}


// ── VPN Chain helpers ─────────────────────────────────────────────────────────
func vpnchainStatus(workDir string) map[string]interface{} {
	runDir := filepath.Join(workDir, "vpnchain", "run")
	pidFile := filepath.Join(runDir, "openvpn.pid")
	logFile := filepath.Join(runDir, "openvpn.log")
	locFile := filepath.Join(runDir, "current.location")

	location := ""
	if b, err := os.ReadFile(locFile); err == nil {
		location = strings.TrimSpace(string(b))
	}

	// Check if openvpn PID is alive
	running := false
	if b, err := os.ReadFile(pidFile); err == nil {
		if pid, err2 := strconv.Atoi(strings.TrimSpace(string(b))); err2 == nil && pid > 0 {
			if err3 := exec.Command("/system/bin/kill", "-0", strconv.Itoa(pid)).Run(); err3 == nil {
				running = true
			}
		}
	}

	connected := false
	exitIP := ""
	if running {
		if b, err := os.ReadFile(logFile); err == nil {
			logStr := string(b)
			connected = strings.Contains(logStr, "Initialization Sequence Completed")
			// Try to extract exit IP from tun0 interface
			if connected {
				if out, err := exec.Command("/system/bin/ip", "-4", "addr", "show", "tun0").Output(); err == nil {
					for _, line := range strings.Split(string(out), "\n") {
						if strings.Contains(line, "inet ") {
							parts := strings.Fields(line)
							for _, p := range parts {
								if strings.Contains(p, ".") && !strings.Contains(p, "/") {
									exitIP = p
									break
								} else if strings.Contains(p, "/") {
									exitIP = strings.Split(p, "/")[0]
									break
								}
							}
						}
					}
				}
			}
		}
	}

	state := "stopped"
	if connected {
		state = "connected"
	} else if running {
		state = "connecting"
	}

	return map[string]interface{}{
		"running":   running,
		"connected": connected,
		"location":  location,
		"ip":        exitIP,
		"state":     state,
	}
}

func listVPNLocations(workDir string) []string {
	configsDir := filepath.Join(workDir, "vpnchain", "configs")
	entries, err := os.ReadDir(configsDir)
	if err != nil {
		return []string{}
	}
	var locs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ovpn") {
			locs = append(locs, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Strings(locs)
	return locs
}


// ── Latency check ─────────────────────────────────────────────────────────────
func measureLatency(target string, socksPort int) int64 {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), 3*time.Second)
	if err != nil {
		return -1
	}
	defer conn.Close()
	// SOCKS5 handshake
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	// Auth negotiation
	conn.Write([]byte{0x05, 0x01, 0x00})
	buf := make([]byte, 2)
	if _, err := conn.Read(buf); err != nil || buf[0] != 0x05 || buf[1] != 0x00 {
		return -1
	}
	// Connect request
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return -1
	}
	portNum, _ := strconv.Atoi(portStr)
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, []byte(host)...)
	req = append(req, byte(portNum>>8), byte(portNum&0xff))
	conn.Write(req)
	resp := make([]byte, 10)
	if _, err := conn.Read(resp); err != nil || resp[0] != 0x05 || resp[1] != 0x00 {
		return -1
	}
	return time.Since(start).Milliseconds()
}

// ── SSE event broadcaster ─────────────────────────────────────────────────────
type sseBroadcaster struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newSSEBroadcaster() *sseBroadcaster {
	return &sseBroadcaster{clients: map[chan string]struct{}{}}
}

func (b *sseBroadcaster) subscribe() chan string {
	ch := make(chan string, 4)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sseBroadcaster) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *sseBroadcaster) broadcast(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
		}
	}
}

var globalSSE = newSSEBroadcaster()


// ── registerExtendedAPI wires all endpoints the v1.0.1 WebUI needs ────────────
func registerExtendedAPI(mux *http.ServeMux, workDir, cfgPath string, st *State,
	clientPtr *atomic.Pointer[issh.Client],
) {
	runDir := filepath.Join(workDir, "run")
	vpnRunDir := filepath.Join(workDir, "vpnchain", "run")
	ovpnScript := filepath.Join(workDir, "scripts", "ovpn.service")
	sshServiceScript := filepath.Join(workDir, "scripts", "ssh.service")

	writeV1 := func(w http.ResponseWriter, ok bool, data interface{}, errMsg string) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{"api_version": "v1", "ok": ok}
		if ok {
			resp["data"] = data
		} else {
			resp["error"] = errMsg
		}
		json.NewEncoder(w).Encode(resp)
	}

	ps := newProfilesStore(workDir)

	// ── /api/v1/status — enriched for WebUI ──────────────────────────────────
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		snap := st.snapshot()
		cfg := getCfgMap(cfgPath)
		socksPort := getCfgInt(cfgPath, "socks_port", 1081)
		redirPort := getCfgInt(cfgPath, "redir_port", 9799)
		runtime := buildEnrichedStatus(snap, cfg, workDir, socksPort, redirPort)
		hotspot := getCfgBool(cfgPath, "hotspot_sharing", false)
		paths := map[string]string{
			"work_dir":    workDir,
			"run_dir":     runDir,
			"webroot":     filepath.Join(workDir, "webroot"),
			"configs_dir": filepath.Join(workDir, "vpnchain", "configs"),
		}
		writeV1(w, true, map[string]interface{}{
			"runtime": runtime,
			"config": map[string]interface{}{
				"network_mode": "redirect",
				"ssh_mode":     getCfgStr(cfgPath, "ssh_mode", "direct"),
				"socks_port":   socksPort,
				"redir_port":   redirPort,
				"channel_pool": true,
				"dns": map[string]interface{}{
					"mode":    "device",
					"servers": []string{},
				},
				"hotspot": map[string]interface{}{
					"enabled": hotspot,
					"tcp":     hotspot,
					"dns":     false,
				},
			},
			"paths": paths,
		}, "")
		// Broadcast SSE status update
		b, _ := json.Marshal(map[string]interface{}{
			"runtime": runtime,
			"config":  map[string]interface{}{"network_mode": "redirect"},
			"paths":   paths,
		})
		go globalSSE.broadcast(string(b))
	})


	// ── /api/v1/diagnostics ───────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		snap := st.snapshot()
		b, _ := json.Marshal(snap)
		var s map[string]interface{}
		json.Unmarshal(b, &s)
		poolSize := getCfgInt(cfgPath, "channel_pool_size", 8)
		activeConns, _ := s["active_connections"].(float64)
		writeV1(w, true, map[string]interface{}{
			"pool": map[string]interface{}{
				"healthy":       poolSize,
				"size":          poolSize,
				"streams":       int(activeConns),
				"max_streams":   64,
				"capacity_hint": fmt.Sprintf("%d connections", poolSize),
			},
		}, "")
	})

	// ── /api/v1/network/public-ip — already in main.go, override with v1.0.1 shape
	mux.HandleFunc("/api/v1/network/public-ip", func(w http.ResponseWriter, r *http.Request) {
		ip, country := st.wanInfo()
		if ip == "" {
			writeV1(w, true, map[string]interface{}{
				"tunnel": map[string]interface{}{
					"ok": false, "error": "resolving — tunnel may not be connected",
				},
				"device": map[string]interface{}{"ok": false},
			}, "")
			return
		}
		writeV1(w, true, map[string]interface{}{
			"tunnel": map[string]interface{}{
				"ok": true, "ip": ip, "country": country,
				"isp": "", "asn": "", "city": "", "region": "", "timezone": "",
				"cached": true,
			},
			"device": map[string]interface{}{"ok": false},
		}, "")
	})

	// ── /api/v1/config — POST to patch hotspot/autostart ─────────────────────
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeV1(w, true, getCfgMap(cfgPath), "")
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405); return
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		pairs := map[string]string{}
		if hotspot, ok := req["hotspot"].(map[string]interface{}); ok {
			enabled := hotspot["enabled"] == true || hotspot["tcp"] == true
			pairs["hotspot_sharing"] = strconv.FormatBool(enabled)
		}
		if pairs != nil && len(pairs) > 0 {
			patchSettingsINI(cfgPath, pairs)
		}
		if restart, _ := req["restart"].(bool); restart {
			go func() {
				time.Sleep(300 * time.Millisecond)
				exec.Command("/system/bin/sh", sshServiceScript, "restart").Run()
			}()
		}
		writeV1(w, true, map[string]string{"status": "applied"}, "")
	})


	// ── /api/v1/profiles ─────────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		data, _ := ps.load()
		writeV1(w, true, data, "")
	})

	// ── /api/v1/profile/save ──────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/profile/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeV1(w, false, nil, err.Error()); return
		}
		ps.mu.Lock()
		data, _ := ps.load()
		profiles, _ := data["profiles"].([]interface{})
		id, _ := req["id"].(string)
		if id == "" {
			id = fmt.Sprintf("profile-%d", time.Now().UnixMilli())
			req["id"] = id
		}
		found := false
		for i, p := range profiles {
			if pm, ok := p.(map[string]interface{}); ok && pm["id"] == id {
				profiles[i] = req; found = true; break
			}
		}
		if !found { profiles = append(profiles, req) }
		data["profiles"] = profiles
		if sel, _ := req["select"].(bool); sel { data["selected_id"] = id }
		ps.save(data)
		ps.mu.Unlock()
		if restart, _ := req["restart"].(bool); restart && (req["select"] == true) {
			applyProfileToSettings(req, cfgPath)
			go func() {
				time.Sleep(300 * time.Millisecond)
				exec.Command("/system/bin/sh", sshServiceScript, "restart").Run()
			}()
		} else if req["select"] == true {
			applyProfileToSettings(req, cfgPath)
		}
		writeV1(w, true, map[string]string{"id": id}, "")
	})

	// ── /api/v1/profile/select ────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/profile/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var req struct { SelectedID string `json:"selected_id"` }
		json.NewDecoder(r.Body).Decode(&req)
		ps.mu.Lock()
		data, _ := ps.load()
		data["selected_id"] = req.SelectedID
		// Apply to settings.ini
		profiles, _ := data["profiles"].([]interface{})
		for _, p := range profiles {
			if pm, ok := p.(map[string]interface{}); ok && pm["id"] == req.SelectedID {
				applyProfileToSettings(pm, cfgPath)
				break
			}
		}
		ps.save(data)
		ps.mu.Unlock()
		writeV1(w, true, map[string]string{"selected_id": req.SelectedID}, "")
	})

	// ── /api/v1/profile/delete ────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/profile/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var req struct { ID string `json:"id"` }
		json.NewDecoder(r.Body).Decode(&req)
		ps.mu.Lock()
		data, _ := ps.load()
		profiles, _ := data["profiles"].([]interface{})
		var keep []interface{}
		for _, p := range profiles {
			if pm, ok := p.(map[string]interface{}); ok && pm["id"] != req.ID {
				keep = append(keep, pm)
			}
		}
		if keep == nil { keep = []interface{}{} }
		data["profiles"] = keep
		if data["selected_id"] == req.ID { data["selected_id"] = "" }
		ps.save(data)
		ps.mu.Unlock()
		writeV1(w, true, map[string]string{"deleted": req.ID}, "")
	})


	// ── VPN Chain endpoints ───────────────────────────────────────────────────
	mux.HandleFunc("/api/v1/vpnchain/status", func(w http.ResponseWriter, r *http.Request) {
		writeV1(w, true, vpnchainStatus(workDir), "")
	})

	mux.HandleFunc("/api/v1/vpnchain/locations", func(w http.ResponseWriter, r *http.Request) {
		// Return array directly — the WebUI v1() helper unwraps data and uses it as array
		writeV1(w, true, listVPNLocations(workDir), "")
	})

	mux.HandleFunc("/api/v1/vpnchain/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var req struct{ Location string `json:"location"` }
		json.NewDecoder(r.Body).Decode(&req)
		if req.Location == "" { writeV1(w, false, nil, "location required"); return }
		// Save current location
		os.MkdirAll(vpnRunDir, 0700)
		os.WriteFile(filepath.Join(vpnRunDir, "current.location"), []byte(req.Location), 0644)
		go exec.Command("/system/bin/sh", ovpnScript, "start", req.Location).Run()
		writeV1(w, true, map[string]string{"scheduled": "start", "location": req.Location}, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		go exec.Command("/system/bin/sh", ovpnScript, "stop").Run()
		writeV1(w, true, map[string]string{"scheduled": "stop"}, "")
	})

	mux.HandleFunc("/api/v1/vpnchain/switch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		var req struct{ Location string `json:"location"` }
		json.NewDecoder(r.Body).Decode(&req)
		if req.Location == "" { writeV1(w, false, nil, "location required"); return }
		os.MkdirAll(vpnRunDir, 0700)
		os.WriteFile(filepath.Join(vpnRunDir, "current.location"), []byte(req.Location), 0644)
		go func() {
			exec.Command("/system/bin/sh", ovpnScript, "stop").Run()
			time.Sleep(500 * time.Millisecond)
			exec.Command("/system/bin/sh", ovpnScript, "start", req.Location).Run()
		}()
		writeV1(w, true, map[string]string{"scheduled": "switch", "location": req.Location}, "")
	})

	// VPN Chain log — serve raw text (WebUI reads as text/plain)
	mux.HandleFunc("/api/v1/vpnchain/log", func(w http.ResponseWriter, r *http.Request) {
		logFile := filepath.Join(workDir, "vpnchain", "run", "openvpn.log")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if b, err := os.ReadFile(logFile); err == nil {
			w.Write(b)
		} else {
			w.Write([]byte("(log not available)"))
		}
	})

	mux.HandleFunc("/api/v1/vpnchain/log/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { w.WriteHeader(405); return }
		logFile := filepath.Join(workDir, "vpnchain", "run", "openvpn.log")
		os.WriteFile(logFile, []byte{}, 0644)
		writeV1(w, true, map[string]string{"cleared": "openvpn.log"}, "")
	})


	// ── Logs — raw text, one endpoint per type ────────────────────────────────
	logPaths := map[string]string{
		"core":    filepath.Join(runDir, "sshcustom.log"),
		"control": filepath.Join(runDir, "control.log"),
		"action":  filepath.Join(runDir, "action.log"),
		"boot":    filepath.Join(runDir, "boot.log"),
		"openvpn": filepath.Join(workDir, "vpnchain", "run", "openvpn.log"),
	}

	serveLog := func(w http.ResponseWriter, logFile string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		f, err := os.Open(logFile)
		if err != nil {
			w.Write([]byte("(log empty or not available)"))
			return
		}
		defer f.Close()
		// Tail last 500 lines
		scanner := bufio.NewScanner(f)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if len(lines) > 500 {
			lines = lines[len(lines)-500:]
		}
		w.Write([]byte(strings.Join(lines, "\n")))
	}

	for logType, logPath := range logPaths {
		lt := logType
		lp := logPath
		mux.HandleFunc("/api/v1/logs/"+lt, func(w http.ResponseWriter, r *http.Request) {
			serveLog(w, lp)
		})
		mux.HandleFunc("/api/v1/logs/"+lt+"/clear", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost { w.WriteHeader(405); return }
			os.WriteFile(lp, []byte{}, 0644)
			writeV1(w, true, map[string]string{"cleared": lt + ".log"}, "")
		})
	}

	// ── /api/v1/latency — measure via SOCKS5 ─────────────────────────────────
	mux.HandleFunc("/api/v1/latency", func(w http.ResponseWriter, r *http.Request) {
		socksPort := getCfgInt(cfgPath, "socks_port", 1081)
		google := measureLatency("google.com:443", socksPort)
		cloudflare := measureLatency("1.1.1.1:443", socksPort)
		writeV1(w, true, map[string]interface{}{
			"google":     google,
			"cloudflare": cloudflare,
		}, "")
	})

	// ── /api/v1/events — SSE stream ───────────────────────────────────────────
	mux.HandleFunc("/api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch := globalSSE.subscribe()
		defer globalSSE.unsubscribe(ch)
		for {
			select {
			case <-r.Context().Done():
				return
			case data := <-ch:
				fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
				flusher.Flush()
			case <-time.After(30 * time.Second):
				fmt.Fprintf(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	})

	// ── Static webroot serving ─────────────────────────────────────────────────
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		webrootDir := filepath.Join(workDir, "webroot")
		if r.URL.Path == "/" || r.URL.Path == "" {
			http.ServeFile(w, r, filepath.Join(webrootDir, "index.html"))
			return
		}
		fp := filepath.Join(webrootDir, filepath.Clean(r.URL.Path))
		if _, err := os.Stat(fp); err == nil {
			http.ServeFile(w, r, fp)
			return
		}
		http.ServeFile(w, r, filepath.Join(webrootDir, "index.html"))
	})
}


// ── settings.ini helpers ──────────────────────────────────────────────────────
func getCfgMap(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{}
	}
	result := map[string]interface{}{}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "if ") ||
			strings.HasPrefix(t, "fi") || strings.HasPrefix(t, "export") ||
			strings.HasPrefix(t, "mkdir") || strings.HasPrefix(t, "log(") ||
			strings.HasPrefix(t, "local ") || strings.HasPrefix(t, "printf") ||
			strings.HasPrefix(t, "}") {
			continue
		}
		eq := strings.IndexByte(t, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(t[:eq])
		val := strings.TrimSpace(t[eq+1:])
		val = strings.Trim(val, `"'`)
		result[key] = val
	}
	return result
}

func getCfgStr(path, key, def string) string {
	m := getCfgMap(path)
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func getCfgInt(path, key string, def int) int {
	s := getCfgStr(path, key, "")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func getCfgBool(path, key string, def bool) bool {
	s := getCfgStr(path, key, "")
	if s == "" {
		return def
	}
	return s == "true" || s == "1"
}
