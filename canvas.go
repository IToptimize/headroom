//go:build windows

package main

import (
	"math"
	"syscall"
	"unsafe"
)

// A tiny anti-aliased software renderer: rounded rects, rings and GDI text,
// drawn into a premultiplied BGRA buffer for icons and layered windows.

type color struct {
	r, g, b uint8
	a       float64
}

func (c color) alpha(a float64) color { c.a *= a; return c }

type canvas struct {
	w, h int
	px   []uint32 // premultiplied 0xAARRGGBB
}

func newCanvas(w, h int) *canvas { return &canvas{w: w, h: h, px: make([]uint32, w*h)} }

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func (c *canvas) blend(x, y int, col color, cov float64) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	a := clamp01(col.a * cov)
	if a <= 0 {
		return
	}
	i := y*c.w + x
	d := c.px[i]
	da := float64(d>>24) / 255
	dr := float64((d>>16)&0xFF) / 255
	dg := float64((d>>8)&0xFF) / 255
	db := float64(d&0xFF) / 255
	k := 1 - a
	oa := a + da*k
	or := float64(col.r)/255*a + dr*k
	og := float64(col.g)/255*a + dg*k
	ob := float64(col.b)/255*a + db*k
	c.px[i] = uint32(oa*255+0.5)<<24 | uint32(or*255+0.5)<<16 | uint32(og*255+0.5)<<8 | uint32(ob*255+0.5)
}

func rrSDF(px, py, x, y, w, h, r float64) float64 {
	qx := math.Abs(px-(x+w/2)) - (w/2 - r)
	qy := math.Abs(py-(y+h/2)) - (h/2 - r)
	out := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
	in := math.Min(math.Max(qx, qy), 0)
	return out + in - r
}

func (c *canvas) bbox(x0, y0, x1, y1 float64, f func(px, py float64) float64, col color) {
	for y := max(0, int(y0)-1); y < min(c.h, int(y1)+2); y++ {
		for x := max(0, int(x0)-1); x < min(c.w, int(x1)+2); x++ {
			if cov := f(float64(x)+0.5, float64(y)+0.5); cov > 0 {
				c.blend(x, y, col, cov)
			}
		}
	}
}

func (c *canvas) fillRoundRect(x, y, w, h, r float64, col color) {
	c.bbox(x, y, x+w, y+h, func(px, py float64) float64 { return clamp01(0.5 - rrSDF(px, py, x, y, w, h, r)) }, col)
}

func (c *canvas) strokeRoundRect(x, y, w, h, r, t float64, col color) {
	c.bbox(x, y, x+w, y+h, func(px, py float64) float64 {
		d := rrSDF(px, py, x, y, w, h, r)
		return clamp01(0.5-d) - clamp01(0.5-(d+t))
	}, col)
}

// shadow draws a soft drop shadow around a rounded rect.
func (c *canvas) shadow(x, y, w, h, r, blur, strength float64) {
	c.bbox(x-blur, y-blur+2, x+w+blur, y+h+blur+2, func(px, py float64) float64 {
		d := rrSDF(px, py-2, x, y, w, h, r)
		if d <= 0 {
			return strength
		}
		f := 1 - clamp01(d/blur)
		return strength * f * f
	}, color{0, 0, 0, 1})
}

func (c *canvas) fillCircle(cx, cy, r float64, col color) {
	c.bbox(cx-r, cy-r, cx+r, cy+r, func(px, py float64) float64 { return clamp01(r + 0.5 - math.Hypot(px-cx, py-cy)) }, col)
}

// ring draws a circular arc from 12 o'clock clockwise covering frac (0..1), with round caps.
func (c *canvas) ring(cx, cy, r, t, frac float64, col color) {
	frac = clamp01(frac)
	if frac <= 0 {
		return
	}
	sweep := frac * 2 * math.Pi
	ex, ey := cx+r*math.Sin(sweep), cy-r*math.Cos(sweep)
	sx, sy := cx, cy-r
	c.bbox(cx-r-t, cy-r-t, cx+r+t, cy+r+t, func(px, py float64) float64 {
		dx, dy := px-cx, py-cy
		radial := clamp01(t/2 + 0.5 - math.Abs(math.Hypot(dx, dy)-r))
		if frac >= 1 {
			return radial
		}
		a := math.Atan2(dx, -dy)
		if a < 0 {
			a += 2 * math.Pi
		}
		cov := 0.0
		if a <= sweep {
			cov = radial
		}
		capd := math.Min(math.Hypot(px-sx, py-sy), math.Hypot(px-ex, py-ey))
		return math.Max(cov, clamp01(t/2+0.5-capd))
	}, col)
}

// ---- text ----

type mask struct {
	w, h int
	a    []uint8
}

func fontFace(weight int) (string, int) {
	switch {
	case weight >= 700:
		return "Segoe UI", 700
	case weight >= 600:
		return "Segoe UI Semibold", 400
	}
	return "Segoe UI", 400
}

// renderText rasterises s with GDI into an alpha mask (cell box, not ink box).
func renderText(s string, px int, weight int, extra int) mask {
	t := syscall.StringToUTF16(s)
	t = t[:len(t)-1]
	if len(t) == 0 {
		return mask{}
	}
	face, wgt := fontFace(weight)
	dc, _, _ := pCreateCompatibleDC.Call(0)
	defer pDeleteDC.Call(dc)
	font, _, _ := pCreateFontW.Call(uintptr(-px), 0, 0, 0, uintptr(wgt), 0, 0, 0, 1, 0, 0, 4, 0, uintptr(unsafe.Pointer(u16(face))))
	oldF, _, _ := pSelectObject.Call(dc, font)
	pSetTextCharacterExtra.Call(dc, uintptr(extra))
	var ext SIZE
	pGetTextExtentPoint32W.Call(dc, uintptr(unsafe.Pointer(&t[0])), uintptr(len(t)), ptr(&ext))
	w, h := int(ext.CX)+2, int(ext.CY)
	if w <= 0 || h <= 0 {
		pSelectObject.Call(dc, oldF)
		pDeleteObject.Call(font)
		return mask{}
	}
	bi := BITMAPINFOHEADER{BiWidth: int32(w), BiHeight: -int32(h), BiPlanes: 1, BiBitCount: 32}
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, ptr(&bi), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	oldB, _, _ := pSelectObject.Call(dc, bmp)
	pSetBkMode.Call(dc, 1)
	pSetTextColor.Call(dc, 0xFFFFFF)
	pTextOutW.Call(dc, 1, 0, uintptr(unsafe.Pointer(&t[0])), uintptr(len(t)))
	pGdiFlush.Call()
	src := unsafe.Slice((*uint32)(bits), w*h)
	m := mask{w: w, h: h, a: make([]uint8, w*h)}
	for i, v := range src {
		m.a[i] = uint8(max((v>>16)&0xFF, (v>>8)&0xFF, v&0xFF))
	}
	pSelectObject.Call(dc, oldB)
	pDeleteObject.Call(bmp)
	pSelectObject.Call(dc, oldF)
	pDeleteObject.Call(font)
	return m
}

// ink returns the bounding box of visible pixels.
func (m mask) ink() (x0, y0, x1, y1 int) {
	x0, y0, x1, y1 = m.w, m.h, -1, -1
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			if m.a[y*m.w+x] > 24 {
				x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
			}
		}
	}
	return
}

func (c *canvas) drawMask(m mask, x, y int, col color) {
	for j := 0; j < m.h; j++ {
		for i := 0; i < m.w; i++ {
			if a := m.a[j*m.w+i]; a > 0 {
				c.blend(x+i, y+j, col, float64(a)/255)
			}
		}
	}
}

const (
	alignLeft = iota
	alignCenter
	alignRight
)

// text draws s with its cell top at y; returns the drawn width.
func (c *canvas) text(s string, x, y float64, px, weight int, col color, align int) int {
	m := renderText(s, px, weight, 0)
	ox := int(math.Round(x))
	switch align {
	case alignCenter:
		ox -= m.w / 2
	case alignRight:
		ox -= m.w
	}
	c.drawMask(m, ox, int(math.Round(y)), col)
	return m.w
}

// textInkCentered draws s visually centred on (cx, cy) using its ink box.
func (c *canvas) textInkCentered(s string, cx, cy float64, px, weight, extra int, col color) {
	m := renderText(s, px, weight, extra)
	x0, y0, x1, y1 := m.ink()
	if x1 < 0 {
		return
	}
	ox := int(math.Round(cx - float64(x0+x1+1)/2))
	oy := int(math.Round(cy - float64(y0+y1+1)/2))
	c.drawMask(m, ox, oy, col)
}

// ---- output ----

func (c *canvas) dib() (dc, bmp, old uintptr) {
	bi := BITMAPINFOHEADER{BiWidth: int32(c.w), BiHeight: -int32(c.h), BiPlanes: 1, BiBitCount: 32}
	bi.BiSize = uint32(unsafe.Sizeof(bi))
	dc, _, _ = pCreateCompatibleDC.Call(0)
	var bits unsafe.Pointer
	bmp, _, _ = pCreateDIBSection.Call(dc, ptr(&bi), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	copy(unsafe.Slice((*uint32)(bits), c.w*c.h), c.px)
	old, _, _ = pSelectObject.Call(dc, bmp)
	return
}

func (c *canvas) icon() uintptr {
	dc, bmp, old := c.dib()
	pSelectObject.Call(dc, old)
	pDeleteDC.Call(dc)
	maskBmp, _, _ := pCreateBitmap.Call(uintptr(c.w), uintptr(c.h), 1, 1, 0)
	ii := ICONINFO{FIcon: 1, HbmMask: maskBmp, HbmColor: bmp}
	ic, _, _ := pCreateIconIndirect.Call(ptr(&ii))
	pDeleteObject.Call(maskBmp)
	pDeleteObject.Call(bmp)
	return ic
}

// present pushes the canvas to a layered window at screen position (x, y).
func (c *canvas) present(hwnd uintptr, x, y int, alpha uint8) {
	screen, _, _ := pGetDC.Call(0)
	dc, bmp, old := c.dib()
	pt := POINT{int32(x), int32(y)}
	sz := SIZE{int32(c.w), int32(c.h)}
	src := POINT{}
	blend := uint32(alpha)<<16 | 1<<24 // AC_SRC_OVER, flags 0, SourceConstantAlpha, AC_SRC_ALPHA
	pUpdateLayeredWindow.Call(hwnd, screen, ptr(&pt), ptr(&sz), dc, ptr(&src), 0, ptr(&blend), 2 /*ULW_ALPHA*/)
	pSelectObject.Call(dc, old)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(dc)
	pReleaseDC.Call(0, screen)
}
