package tray

import "math"

// paintDot draws the attention dot (a white ring around an orange-red disc) in the bottom-right corner of a BGRA image of w by h
// pixels, blending its soft edge with what is already there. A buffer too small for w by h is left alone.
func paintDot(pix []byte, w, h int) {
	if w < 8 || h < 8 || len(pix) < w*h*4 {
		return
	}
	r := float64(w) / 4
	if r < 3 {
		r = 3
	}
	cx, cy := float64(w)-r, float64(h)-r
	inner := r - 1.2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			if d > r+0.5 {
				continue
			}
			i := (y*w + x) * 4
			blend(pix[i:i+4], 255, 255, 255, coverage(r-d))
			blend(pix[i:i+4], 0x0F, 0x48, 0xD9, coverage(inner-d)) // B, G, R of #D9480F
		}
	}
}

// coverage turns "how far inside the edge" into 0..1 (one pixel of soft edge).
func coverage(inside float64) float64 {
	return math.Max(0, math.Min(1, inside+0.5))
}

// blend puts colour b, g, r over px (BGRA) with the given coverage, alpha included.
func blend(px []byte, b, g, r byte, cov float64) {
	if cov <= 0 {
		return
	}
	mix := func(old, nu byte) byte { return byte(float64(nu)*cov + float64(old)*(1-cov) + 0.5) }
	px[0], px[1], px[2] = mix(px[0], b), mix(px[1], g), mix(px[2], r)
	px[3] = mix(px[3], 255)
}
