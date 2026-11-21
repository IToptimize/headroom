//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Optional (off by default): read Claude usage straight from Anthropic, so
// the orange icon works without Claude Code's status line (e.g. the VS Code
// panel, or when Claude Code is closed). Uses the OAuth token Claude Code
// stores in ~/.claude/.credentials.json and the *undocumented* endpoint
// GET https://api.anthropic.com/api/oauth/usage, as community tools do.
// The token is only read: never refreshed, written, logged or sent anywhere
// but api.anthropic.com. At most one request per 2-5 minutes (user choice).

var (
	winhttp               = syscall.NewLazyDLL("winhttp.dll")
	pWinHttpOpen          = winhttp.NewProc("WinHttpOpen")
	pWinHttpSetTimeouts   = winhttp.NewProc("WinHttpSetTimeouts")
	pWinHttpConnect       = winhttp.NewProc("WinHttpConnect")
	pWinHttpOpenRequest   = winhttp.NewProc("WinHttpOpenRequest")
	pWinHttpSendRequest   = winhttp.NewProc("WinHttpSendRequest")
	pWinHttpReceiveResp   = winhttp.NewProc("WinHttpReceiveResponse")
	pWinHttpQueryHeaders  = winhttp.NewProc("WinHttpQueryHeaders")
	pWinHttpReadData      = winhttp.NewProc("WinHttpReadData")
	pWinHttpCloseHandle   = winhttp.NewProc("WinHttpCloseHandle")
	claudeFetchKick       = make(chan struct{}, 1)
	claudeFetchStatus     string // shown under the setting
	claudeFetchStatusLock = make(chan struct{}, 1)
)

// fetchEvery: the user's choice; the community source warns that polling
// more often than ~3 minutes gets rate-limited, so 2 minutes is the floor.
func fetchEvery() time.Duration { return time.Duration(cfg.ClaudeEvery) * time.Minute }

func setFetchStatus(s string) {
	claudeFetchStatusLock <- struct{}{}
	claudeFetchStatus = s
	<-claudeFetchStatusLock
}

func getFetchStatus() string {
	claudeFetchStatusLock <- struct{}{}
	defer func() { <-claudeFetchStatusLock }()
	return claudeFetchStatus
}

// httpsGet does a GET with WinHTTP (system proxy and certificates).
func httpsGet(host, path, headers string) (int, []byte, error) {
	sess, _, err := pWinHttpOpen.Call(uintptr(unsafe.Pointer(u16("Headroom/"+appVersion))), 4 /*AUTOMATIC_PROXY*/, 0, 0, 0)
	if sess == 0 {
		sess, _, err = pWinHttpOpen.Call(uintptr(unsafe.Pointer(u16("Headroom/"+appVersion))), 0, 0, 0, 0)
		if sess == 0 {
			return 0, nil, fmt.Errorf("WinHttpOpen: %v", err)
		}
	}
	defer pWinHttpCloseHandle.Call(sess)
	pWinHttpSetTimeouts.Call(sess, 10000, 10000, 10000, 15000)
	conn, _, err := pWinHttpConnect.Call(sess, uintptr(unsafe.Pointer(u16(host))), 443, 0)
	if conn == 0 {
		return 0, nil, fmt.Errorf("connect: %v", err)
	}
	defer pWinHttpCloseHandle.Call(conn)
	req, _, err := pWinHttpOpenRequest.Call(conn, uintptr(unsafe.Pointer(u16("GET"))), uintptr(unsafe.Pointer(u16(path))), 0, 0, 0, 0x00800000 /*SECURE*/)
	if req == 0 {
		return 0, nil, fmt.Errorf("request: %v", err)
	}
	defer pWinHttpCloseHandle.Call(req)
	h := syscall.StringToUTF16(headers)
	if r, _, err := pWinHttpSendRequest.Call(req, uintptr(unsafe.Pointer(&h[0])), uintptr(len(h)-1), 0, 0, 0, 0); r == 0 {
		return 0, nil, fmt.Errorf("send: %v", err)
	}
	for i := range h { // don't leave the token lying around
		h[i] = 0
	}
	if r, _, err := pWinHttpReceiveResp.Call(req, 0); r == 0 {
		return 0, nil, fmt.Errorf("receive: %v", err)
	}
	var status, sz uint32 = 0, 4
	pWinHttpQueryHeaders.Call(req, 19|0x20000000 /*STATUS_CODE|FLAG_NUMBER*/, 0, ptr(&status), ptr(&sz), 0)
	var body []byte
	buf := make([]byte, 8192)
	for len(body) < 1<<20 {
		var n uint32
		if r, _, _ := pWinHttpReadData.Call(req, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), ptr(&n)); r == 0 || n == 0 {
			break
		}
		body = append(body, buf[:n]...)
	}
	return int(status), body, nil
}

var errLoginExpired = errors.New("login expired")

var claudePlan string // subscription type from the login file, e.g. "pro"

func readClaudeToken() (string, error) {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return "", errors.New("no Claude login found on this PC")
	}
	var c struct {
		OAuth struct {
			AccessToken string  `json:"accessToken"`
			ExpiresAt   float64 `json:"expiresAt"` // ms
			Plan        string  `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &c) != nil || c.OAuth.AccessToken == "" {
		return "", errors.New("no Claude login found on this PC")
	}
	claudePlan = c.OAuth.Plan
	if c.OAuth.ExpiresAt > 0 && time.Now().UnixMilli() > int64(c.OAuth.ExpiresAt) {
		return "", errLoginExpired
	}
	return c.OAuth.AccessToken, nil
}

type usageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

func parseResets(s string) int64 {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// fetchClaudeUsage returns how long to wait before the next attempt.
func fetchClaudeUsage() time.Duration {
	tok, err := readClaudeToken()
	if err == errLoginExpired {
		setFetchStatus("Login expired: open Claude once to refresh it")
		return fetchEvery()
	}
	if err != nil {
		setFetchStatus(err.Error())
		return 15 * time.Minute
	}
	refreshClaudePlan(tok)
	status, body, err := httpsGet("api.anthropic.com", "/api/oauth/usage",
		"Authorization: Bearer "+tok+"\r\nanthropic-beta: oauth-2025-04-20\r\nContent-Type: application/json\r\n")
	tok = ""
	switch {
	case err != nil:
		dbg("claude usage fetch: %v", err)
		setFetchStatus("Couldn't reach Anthropic (offline?)")
		return fetchEvery()
	case status == 401 || status == 403:
		setFetchStatus("Login not accepted: open Claude once to refresh it")
		return 10 * time.Minute
	case status == 429:
		setFetchStatus("Anthropic asked to slow down; retrying in 15 min")
		return 15 * time.Minute
	case status != 200:
		dbg("claude usage fetch: HTTP %d", status)
		setFetchStatus(fmt.Sprintf("Unexpected answer (HTTP %d)", status))
		return 15 * time.Minute
	}
	var r struct {
		FiveHour *usageWindow `json:"five_hour"`
		SevenDay *usageWindow `json:"seven_day"`
	}
	var all map[string]json.RawMessage
	json.Unmarshal(body, &all)
	if json.Unmarshal(body, &r) != nil {
		setFetchStatus("Unexpected answer format")
		return 15 * time.Minute
	}
	u := claudeUsage{Updated: time.Now().Unix(), Plan: currentPlan()}
	if old, ok := readClaudeUsage(); ok {
		u.ContextUsed = old.ContextUsed
	}
	if w := r.FiveHour; w != nil && w.Utilization != nil {
		u.HasSession, u.SessionUsed, u.SessionEnds = true, *w.Utilization, parseResets(w.ResetsAt)
	}
	if w := r.SevenDay; w != nil && w.Utilization != nil {
		u.HasWeek, u.WeekUsed, u.WeekEnds = true, *w.Utilization, parseResets(w.ResetsAt)
	}
	// any other weekly limit (seven_day_fable, seven_day_opus...) with a value
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	dbg("claude usage keys: %s", strings.Join(keys, ", "))
	// the per-model limits seem to live in these newer fields; log their
	// shape (usage numbers only, no account data) so a report shows it
	if forceShapeLog.Swap(false) || time.Since(lastShapeLog) > 30*time.Minute {
		lastShapeLog = time.Now()
		for _, k := range keys {
			if strings.HasPrefix(k, "seven_day") || k == "limits" || k == "usage_scopes" || k == "weekly_scoped_shares" {
				v := string(all[k])
				if len(v) > 700 {
					v = v[:700] + "…"
				}
				dbg("claude usage %s = %s", k, v)
			}
		}
	}
	for _, k := range keys {
		name, ok := strings.CutPrefix(k, "seven_day_")
		if !ok || strings.Contains(name, "oauth") {
			continue
		}
		var w usageWindow
		if json.Unmarshal(all[k], &w) != nil || w.Utilization == nil {
			continue
		}
		u.Extra = append(u.Extra, claudeExtra{Name: limitName(name), Used: *w.Utilization, Ends: parseResets(w.ResetsAt)})
	}
	// a separate Fable weekly limit only exists on Max/Ultra, so the plan
	// can't be Pro any more even if the login file still says so
	if strings.EqualFold(u.Plan, "pro") {
		for _, e := range u.Extra {
			if strings.EqualFold(e.Name, "fable") {
				u.Plan = "max"
			}
		}
	}
	u.HasLimits = u.HasSession || u.HasWeek
	if !u.HasLimits {
		setFetchStatus("No usage limits reported for this account")
		return fetchEvery()
	}
	if out, err := json.Marshal(u); err == nil {
		os.MkdirAll(filepath.Dir(claudeUsageFile()), 0o755)
		tmp := claudeUsageFile() + ".tmp"
		if os.WriteFile(tmp, out, 0o644) == nil {
			os.Rename(tmp, claudeUsageFile())
		}
	}
	setFetchStatus("Updated " + time.Now().Format("15:04"))
	dbg("claude usage: session %.0f%% week %.0f%%", u.SessionUsed, u.WeekUsed)
	return fetchEvery()
}

func claudeFetchLoop() {
	for {
		wait := fetchEvery()
		if cfg.ClaudeFetch {
			wait = fetchClaudeUsage()
			pPostMessageW.Call(hwnd, wmClaudeCheck, 0, 0)
		}
		select {
		case <-claudeFetchKick:
		case <-time.After(wait):
		}
	}
}

func kickClaudeFetch() {
	select {
	case claudeFetchKick <- struct{}{}:
	default:
	}
}

// limitName turns "fable" / "sonnet_4" into "Fable" / "Sonnet 4".
func limitName(k string) string {
	parts := strings.Split(k, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// Plan detection. The login file's subscriptionType is only rewritten when
// Claude Code logs in again, so after an upgrade it can say "pro" for hours.
// The account profile (the same unofficial API family as the usage endpoint)
// knows right away; we ask it at most every 30 minutes and fall back to the
// login file when it doesn't answer or the format is unknown.
var (
	profilePlan    string
	profileChecked time.Time
	forcePlanCheck atomic.Bool // "Refresh now" checks the plan too
	lastShapeLog   time.Time
	forceShapeLog  atomic.Bool // set at start and by "Report…"
)

func currentPlan() string {
	if profilePlan != "" {
		return profilePlan
	}
	return claudePlan
}

func refreshClaudePlan(tok string) {
	if time.Since(profileChecked) < 30*time.Minute && !forcePlanCheck.Swap(false) {
		return
	}
	profileChecked = time.Now()
	status, body, err := httpsGet("api.anthropic.com", "/api/oauth/profile",
		"Authorization: Bearer "+tok+"\r\nanthropic-beta: oauth-2025-04-20\r\nContent-Type: application/json\r\n")
	if err != nil || status != 200 {
		dbg("claude profile: status %d err %v", status, err)
		return
	}
	if p := planFromProfile(body); p != "" {
		profilePlan = p
		dbg("claude profile: plan %s", p)
	}
}

// planFromProfile reads the plan from the profile answer without relying on
// one exact shape: rate_limit_tier ("default_claude_max_20x"), then
// organization_type ("claude_max"), then has_claude_max / has_claude_pro.
func planFromProfile(body []byte) string {
	var r struct {
		Account struct {
			HasMax *bool `json:"has_claude_max"`
			HasPro *bool `json:"has_claude_pro"`
		} `json:"account"`
		Org struct {
			Type string `json:"organization_type"`
			Tier string `json:"rate_limit_tier"`
		} `json:"organization"`
	}
	if json.Unmarshal(body, &r) != nil {
		return ""
	}
	for _, f := range []string{strings.ToLower(r.Org.Tier), strings.ToLower(r.Org.Type)} {
		switch {
		case strings.Contains(f, "ultra"):
			return "ultra"
		case strings.Contains(f, "max_20x"):
			return "max 20x"
		case strings.Contains(f, "max_5x"):
			return "max 5x"
		case strings.Contains(f, "max"):
			return "max"
		case strings.Contains(f, "team"):
			return "team"
		case strings.Contains(f, "enterprise"):
			return "enterprise"
		case strings.Contains(f, "pro"):
			return "pro"
		}
	}
	switch {
	case r.Account.HasMax != nil && *r.Account.HasMax:
		return "max"
	case r.Account.HasPro != nil && *r.Account.HasPro:
		return "pro"
	}
	return ""
}
