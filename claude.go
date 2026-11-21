//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Claude usage. Claude Code pipes a JSON blob to a status-line command on
// every update; for Pro/Max plans it includes rate_limits.five_hour and
// rate_limits.seven_day (used_percentage, resets_at). `Headroom.exe
// --statusline` prints two rings for the terminal and saves the numbers to
// %LOCALAPPDATA%\Headroom\claude-usage.json, which the tray app reads.

type claudeUsage struct {
	Updated     int64   `json:"updated"`           // unix time of the last status-line call
	HasLimits   bool    `json:"has_limits"`        // rate_limits were present
	SessionUsed float64 `json:"session_used"`      // 0-100
	SessionEnds int64   `json:"session_resets_at"` // unix
	WeekUsed    float64 `json:"week_used"`         // 0-100
	WeekEnds    int64   `json:"week_resets_at"`    // unix
	HasSession  bool    `json:"has_session"`
	HasWeek     bool    `json:"has_week"`
	ContextUsed float64 `json:"context_used,omitempty"`
	Plan        string  `json:"plan,omitempty"` // "pro", "max"... (from the login, when known)
	// Extra weekly limits the account reports besides the all-models one
	// (e.g. a separate Fable limit on Max), from the usage fetch only.
	Extra []claudeExtra `json:"extra,omitempty"`
}

type claudeExtra struct {
	Name string  `json:"name"` // "Fable", "Opus"...
	Used float64 `json:"used"`
	Ends int64   `json:"resets_at"`
}

func claudeUsageFile() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Headroom", "claude-usage.json")
}

// epoch accepts a unix number or an RFC 3339 string.
func epoch(raw json.RawMessage) int64 {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return int64(n)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Unix()
		}
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 0
}

type rlWindow struct {
	Used   *float64        `json:"used_percentage"`
	Resets json.RawMessage `json:"resets_at"`
}

func parseStatusInput(b []byte) claudeUsage {
	var in struct {
		RateLimits *struct {
			FiveHour *rlWindow `json:"five_hour"`
			SevenDay *rlWindow `json:"seven_day"`
		} `json:"rate_limits"`
		Context *struct {
			Used *float64 `json:"used_percentage"`
		} `json:"context_window"`
	}
	_ = json.Unmarshal(b, &in)
	u := claudeUsage{Updated: time.Now().Unix()}
	if in.Context != nil && in.Context.Used != nil {
		u.ContextUsed = *in.Context.Used
	}
	if rl := in.RateLimits; rl != nil {
		if w := rl.FiveHour; w != nil && w.Used != nil {
			u.HasSession, u.SessionUsed, u.SessionEnds = true, *w.Used, epoch(w.Resets)
		}
		if w := rl.SevenDay; w != nil && w.Used != nil {
			u.HasWeek, u.WeekUsed, u.WeekEnds = true, *w.Used, epoch(w.Resets)
		}
		u.HasLimits = u.HasSession || u.HasWeek
	}
	return u
}

// pie picks a filling circle for the share left.
func pie(left float64) string {
	switch {
	case left >= 87.5:
		return "●"
	case left >= 62.5:
		return "◕"
	case left >= 37.5:
		return "◑"
	case left >= 12.5:
		return "◔"
	}
	return "○"
}

func untilText(end int64, now time.Time) string {
	if end <= 0 {
		return ""
	}
	d := time.Unix(end, 0).Sub(now)
	switch {
	case d <= 0:
		return "resetting"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes())+1)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return time.Unix(end, 0).Format("Mon 15:04")
}

func ansi(c color, s string) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", c.r, c.g, c.b, s)
}

var (
	claudeOrange = color{217, 119, 87, 1}  // #D97757
	claudePeach  = color{240, 184, 150, 1} // lighter, for the weekly window
)

func claudeTint(left float64, week bool) color {
	if left < 10 {
		return colRed
	}
	if week {
		return claudePeach
	}
	return claudeOrange
}

// statusLineText renders the line Claude Code shows under the prompt.
func statusLineText(u claudeUsage, now time.Time) string {
	dim := func(s string) string { return "\x1b[2m" + s + "\x1b[0m" }
	if !u.HasLimits {
		s := ansi(claudeOrange, "◌") + dim(" Claude usage appears after the first reply")
		if u.ContextUsed > 0 {
			s += dim(fmt.Sprintf("  ·  ctx %.0f%%", u.ContextUsed))
		}
		return s
	}
	var parts []string
	if u.HasSession {
		left := math.Max(0, 100-u.SessionUsed)
		p := ansi(claudeTint(left, false), pie(left)) + fmt.Sprintf(" 5h %.0f%% left", left)
		if t := untilText(u.SessionEnds, now); t != "" {
			p += dim(" · " + t)
		}
		parts = append(parts, p)
	}
	if u.HasWeek {
		left := math.Max(0, 100-u.WeekUsed)
		p := ansi(claudeTint(left, true), pie(left)) + fmt.Sprintf(" week %.0f%% left", left)
		if t := untilText(u.WeekEnds, now); t != "" {
			p += dim(" · " + t)
		}
		parts = append(parts, p)
	}
	s := strings.Join(parts, "   ")
	if u.ContextUsed > 0 {
		s += dim(fmt.Sprintf("   ctx %.0f%%", u.ContextUsed))
	}
	return s
}

// runStatusLine is `Headroom.exe --statusline`: called by Claude Code.
func runStatusLine() {
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	u := parseStatusInput(b)
	// keep the last known limits if this call didn't carry them (e.g. before the first reply)
	if !u.HasLimits {
		if old, ok := readClaudeUsage(); ok && old.HasLimits {
			old.Updated, old.ContextUsed = u.Updated, u.ContextUsed
			u = old
		}
	} else if old, ok := readClaudeUsage(); ok {
		u.Plan, u.Extra = old.Plan, old.Extra
	}
	if out, err := json.Marshal(u); err == nil {
		os.MkdirAll(filepath.Dir(claudeUsageFile()), 0o755)
		tmp := claudeUsageFile() + ".tmp"
		if os.WriteFile(tmp, out, 0o644) == nil {
			os.Rename(tmp, claudeUsageFile())
		}
	}
	fmt.Fprintln(os.Stdout, statusLineText(u, time.Now()))
}

func readClaudeUsage() (claudeUsage, bool) {
	var u claudeUsage
	b, err := os.ReadFile(claudeUsageFile())
	if err != nil || json.Unmarshal(b, &u) != nil {
		return u, false
	}
	return u, true
}

// claudeView turns the saved numbers into a panel row; windows whose reset
// time has passed count as fresh (100 % left) until Claude Code reports again.
func claudeView(now time.Time) (View, bool) {
	u, ok := readClaudeUsage()
	if !ok || !u.HasLimits || now.Sub(time.Unix(u.Updated, 0)) > 8*24*time.Hour {
		return View{}, false
	}
	v := View{ID: "claude", Name: "Claude", Kind: "Claude Code", Tested: true, OK: true, Updated: time.Unix(u.Updated, 0)}
	stale := now.Sub(time.Unix(u.Updated, 0)) > 10*time.Minute
	part := func(label string, has bool, used float64, ends int64, tint int) {
		if !has {
			return
		}
		left := math.Max(0, 100-used)
		sub := ""
		if ends > 0 && now.Unix() >= ends {
			left, sub = 100, "reset"
		} else if t := untilText(ends, now); t != "" {
			if time.Unix(ends, 0).Sub(now) >= 24*time.Hour {
				sub = "resets " + t // "resets Thu 09:00"
			} else {
				sub = "resets in " + t
			}
		}
		p := Part{Label: label, Level: int(math.Round(left)), Sub: sub, Tint: tint}
		if left < 0.5 && ends > now.Unix() {
			p.Level = 0
			p.Note = "Used up"
			p.Sub = "ready in " + untilText(ends, now)
		} else if stale && sub != "reset" {
			p.Note = "as of " + time.Unix(u.Updated, 0).Format("15:04")
		}
		v.Parts = append(v.Parts, p)
	}
	part("Session (5 h)", u.HasSession, u.SessionUsed, u.SessionEnds, 1)

	part("Week", u.HasWeek, u.WeekUsed, u.WeekEnds, 2)
	for _, e := range u.Extra {
		part("Week · "+e.Name, true, e.Used, e.Ends, 2)
	}
	if len(v.Parts) == 0 {
		return View{}, false
	}
	v.Level = v.Parts[0].Level
	return v, true
}
