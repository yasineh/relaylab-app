# RelayLab Complete Brand & Design System Specifications

## 1. Brand Identity Overview

RelayLab is an event-driven developer platform built for real-time webhook testing, payload inspection, security proxying, and background replaying.

### Official Emblem: The Dual-Arrow "R"

- **Geometry:** Custom uppercase "R" featuring a rounded upper loop and dual arrow terminals on both bottom legs.
- **Left Leg:** Vertical spine curving into a downward-pointing arrowhead (signifying event ingestion).
- **Right Leg:** Diagonal leg terminating in an outbound arrow (signifying event routing & replay dispatch).

---

## 2. Color Palette & Design Tokens

### Primary Brand Tokens

- **RelayLab Blue:** `#2563EB` (Primary buttons, active states, main brand mark)
- **Relay Dark Slate:** `#0F172A` (Primary typography, dark theme background, headers)
- **Lab Gray:** `#64748B` (Secondary text, subtitles, borders)
- **Canvas Background (Light):** `#F8FAFC`
- **Surface Dark:** `#1E293B`

### Status Tokens

- **Success Green:** `#10B981` (HTTP 200 OK, active delivery)
- **Warning Orange:** `#F59E0B` (HTTP 4xx, retries)
- **Error Red:** `#EF4444` (HTTP 5xx, SSRF blocking)

---

### Implementation in the web app

The tokens live in `web/src/index.css` as CSS variables mapped onto shadcn/ui
names (`--primary`, `--foreground`, ...). To meet WCAG AA contrast (4.5:1) for
small text:

- On light backgrounds, status text uses darker shades of the same hue: success `#047857`, warning `#B45309`, error `#DC2626`, muted text `#475569`. The exact brand values stay available as `--brand-success`, `--brand-warning`, and `--brand-error` for icons and fills.
- On the dark theme, primary buttons use `#3B82F6` (the blue of the dark logo) with Relay Dark Slate text, and error text uses `#F87171`.

---

## 3. Brand Assets in This Repository

| File | Use |
|---|---|
| `web/public/favicon.svg` | Browser tab icon (served at `/favicon.svg`, linked from `web/index.html`) |
| `web/src/assets/brand/logo-icon.svg` | Standalone "R" emblem (app header, small placements) |
| `web/src/assets/brand/logo-horizontal-light.svg` | Emblem + wordmark on light backgrounds |
| `web/src/assets/brand/logo-horizontal-dark.svg` | Emblem + wordmark on dark backgrounds (includes its own dark slate background) |

Import logos from `web/src/assets/brand/` in components so Vite fingerprints them.
Files in `web/public/` are served as-is at the site root.
