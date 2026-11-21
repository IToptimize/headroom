# Changelog

## 1.4.4 beta
- No more "Estimating time left…" placeholder; the time appears once there's a real estimate.
- Reports include the shape of Claude's per-model limit fields, to find where the Fable limit lives.

## 1.4.3 beta
- Claude panel and hover card now count the same way as the tray icon and say so ("89% left" / "11% used", following the Look setting). Before, the panel showed % left and the card % used, which looked like different numbers.
- The plan (Pro/Max/Ultra) is detected from the account, so an upgrade shows without logging out and in. "Refresh now" checks it immediately.

## 1.4.2 beta
- Headroom and Claude are now fully separate: the headset panel only shows headsets, and clicking the orange icon opens its own Claude panel ("Show in the headset panel" removed).

## 1.4.1 beta
- Shows extra weekly limits the account reports (e.g. the separate Fable limit on Max) in the hover card and panel, when "Read usage from my Claude login" is on.

## 1.4 beta
- Claude rings colour by usage in both looks: purple while there's room, then yellow, orange and red; inner ring = weekly, a full red ring when used up.
- Redesigned hover card ("Claude usage", plan pill, bars in the same usage colours, reset time and countdown).
- Hover card now sits right above the tray icon (it used to appear in a corner) and only appears after resting on the icon for 0.5 s.
- Calmer green for battery rings, toggles and the green accent.

## 1.3 beta
- Claude look option: the orange icon fills with what's *used* in Claude's colours (amber when high, red when nearly out).
- A used-up limit shows as a full red ring (both looks) - before, 0 % left still showed a small sliver.
- Hover card on the orange icon, modelled on Claude's usage popup (5-hour and weekly bars, reset times); clicking still opens the panel.
- When the 5-hour limit runs out: "ready in …" countdown, a pop-up, and another pop-up when it's ready again (can be switched off).
- Settings split into Headroom and Claude tabs; right-click the orange icon for its own menu and Claude settings.

## 1.2.1 beta
- Choose how often Claude usage is fetched: 2, 3 (default) or 5 minutes.
- Copy setup only quotes the exe path when it has spaces (PowerShell treats a quoted path as text).

## 1.2 beta
- Opt-in "Read usage from my Claude login": fetches Claude usage every 5 minutes via the login Claude Code stores (undocumented endpoint), so the orange ring works with the VS Code panel and when Claude Code is closed. Token is read-only, never logged, only sent to api.anthropic.com.

## 1.1 beta
- Claude Code usage: `Headroom.exe --statusline` shows two rings (5-hour session, week) under the Claude Code prompt and feeds an orange double-ring tray icon plus a Claude row in the panel.
- Settings in two columns, new Claude Code section (tray / panel toggles, Copy setup).

## 1.0 beta
- Renamed from "HyperX Battery" to **Headroom**; automatic migration of settings, learned data, startup entry and install folder.
- App icon, manifest and version info embedded in the exe.
- **Report my headset**: one-click text report (devices, readings, recent log), copied to the clipboard.
- **Always show in taskbar** (Windows 11), turned on automatically once.

## 0.9.x beta
- SteelSeries (Arctis family, Nova Pro Wireless incl. spare battery in the base), Corsair Void family, Sony INZONE H9 II / H5, MCHOSE X9.
- Time to full for the Nova Pro spare battery; time text under each battery's %.
- Step-timing estimates for coarse-reporting headsets; battery swaps show immediately.
- Fix: false "charging" after the Cloud Flight was off for a while.

## 0.8 beta
- Settings panel: theme, colours/accent, tray icon style, compact layout, pop-up levels, refresh speed.

## 0.7 beta
- AirPods / Beats (left, right, case), Bluetooth headsets, all HyperX wireless families.
- Multi-device panel, per-device learning and pop-ups.

## Earlier
- Cloud Flight Wireless support, voltage curve, charging detection, flyout panel, ring icon, silent pop-ups, adaptive refresh.
