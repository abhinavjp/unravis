//go:build ignore

// Renders the Unravis mark (three bars on a dark tile, as in web/src/brand BrandMark) and writes
// an .ico plus a Windows resource object (.syso) that gives a program that icon.
// Run from the repo root: go run tools/icon/main.go <dir-for-ico> <syso-out> [<syso-out> ...]
// The outputs are committed, so a normal build needs no extra step.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

var (
	tile   = color.NRGBA{0x10, 0x17, 0x14, 0xff}
	edge   = color.NRGBA{0x1a, 0x26, 0x21, 0xff}
	accent = color.NRGBA{0x3f, 0xd6, 0xb0, 0xff}
)

func render(n int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	f := float64(n) / 26
	r := 4 * f
	inTile := func(x, y float64) bool {
		lo, hi := r, float64(n)-r
		cx := min(max(x, lo), hi)
		cy := min(max(y, lo), hi)
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			px, py := float64(x)+.5, float64(y)+.5
			if !inTile(px, py) {
				continue
			}
			c := tile
			if !inTile(px-1.2*f, py) || !inTile(px+1.2*f, py) || !inTile(px, py-1.2*f) || !inTile(px, py+1.2*f) {
				c = edge
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// Bars as in BrandMark: 26 px tile, 5 px padding, 2 px gaps, heights 7/14/10, opacity .55/1/.8.
	barW := (26 - 10 - 4) / 3.0 * f
	for i, h := range []float64{7, 14, 10} {
		x0 := 5*f + float64(i)*(barW+2*f)
		y1 := 21 * f
		c := accent
		c.A = []uint8{140, 255, 204}[i]
		draw.Draw(img, image.Rect(int(x0+.5), int(y1-h*f+.5), int(x0+barW+.5), int(y1+.5)), image.NewUniform(c), image.Point{}, draw.Over)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func le(b *bytes.Buffer, v ...any) {
	for _, x := range v {
		_ = binary.Write(b, binary.LittleEndian, x)
	}
}

func dim(s int) byte {
	if s >= 256 {
		return 0
	}
	return byte(s)
}

func icoFile(sizes []int, pngs [][]byte) []byte {
	var out bytes.Buffer
	le(&out, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		out.Write([]byte{dim(s), dim(s), 0, 0})
		le(&out, [2]uint16{1, 32}, [2]uint32{uint32(len(pngs[i])), uint32(off)})
		off += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes()
}

// sysoFile builds an amd64 COFF object with one .rsrc section: RT_ICON images 1..n and RT_GROUP_ICON 1.
func sysoFile(sizes []int, pngs [][]byte) []byte {
	n := len(sizes)
	var group bytes.Buffer
	le(&group, [3]uint16{0, 1, uint16(n)})
	for i, s := range sizes {
		group.Write([]byte{dim(s), dim(s), 0, 0})
		le(&group, [2]uint16{1, 32}, uint32(len(pngs[i])), uint16(i+1))
	}
	blobs := append(append([][]byte{}, pngs...), group.Bytes()) // resources 0..n-1 icons, n the group

	// Layout: root dir, ICON type dir, GROUP type dir, one language dir per resource, data entries, blobs.
	iconDir := 16 + 2*8
	groupDir := iconDir + 16 + n*8
	langDirs := groupDir + 16 + 8
	dataEntries := langDirs + (n+1)*24
	offs := make([]int, n+1)
	pos := dataEntries + (n+1)*16
	for i, b := range blobs {
		offs[i] = pos
		pos += (len(b) + 3) &^ 3
	}
	dir := func(b *bytes.Buffer, ids int) { le(b, [2]uint32{}, [2]uint16{}, uint16(0), uint16(ids)) }
	var sec bytes.Buffer
	dir(&sec, 2)
	le(&sec, [2]uint32{3, 0x80000000 | uint32(iconDir)}, [2]uint32{14, 0x80000000 | uint32(groupDir)})
	dir(&sec, n)
	for i := 0; i < n; i++ {
		le(&sec, [2]uint32{uint32(i + 1), 0x80000000 | uint32(langDirs+i*24)})
	}
	dir(&sec, 1)
	le(&sec, [2]uint32{1, 0x80000000 | uint32(langDirs+n*24)})
	for i := 0; i <= n; i++ {
		dir(&sec, 1)
		le(&sec, [2]uint32{0x0409, uint32(dataEntries + i*16)})
	}
	var relocs []uint32
	for i, b := range blobs {
		relocs = append(relocs, uint32(sec.Len()))
		le(&sec, uint32(offs[i]), uint32(len(b)), [2]uint32{0, 0})
	}
	for _, b := range blobs {
		sec.Write(b)
		sec.Write(make([]byte, (4-len(b)%4)%4))
	}

	const hdr, shdr = 20, 40
	rawAt := hdr + shdr
	relAt := rawAt + sec.Len()
	symAt := relAt + 10*len(relocs)
	var out bytes.Buffer
	le(&out, uint16(0x8664), uint16(1), uint32(0), uint32(symAt), uint32(1), uint16(0), uint16(0))
	out.Write([]byte(".rsrc\x00\x00\x00"))
	le(&out, uint32(0), uint32(0), uint32(sec.Len()), uint32(rawAt), uint32(relAt), uint32(0), uint16(len(relocs)), uint16(0), uint32(0x40000040))
	out.Write(sec.Bytes())
	for _, r := range relocs {
		le(&out, r, uint32(0), uint16(3)) // IMAGE_REL_AMD64_ADDR32NB against the .rsrc symbol
	}
	out.Write([]byte(".rsrc\x00\x00\x00"))
	le(&out, uint32(0), uint16(1), uint16(0), uint8(3), uint8(0)) // static symbol in section 1
	le(&out, uint32(4))                                           // empty string table
	return out.Bytes()
}

func main() {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, s := range sizes {
		pngs = append(pngs, render(s))
	}
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(os.WriteFile(filepath.Join(os.Args[1], "unravis.ico"), icoFile(sizes, pngs), 0o644))
	must(os.WriteFile(filepath.Join(os.Args[1], "unravis-256.png"), pngs[len(pngs)-1], 0o644))
	for _, p := range os.Args[2:] {
		must(os.WriteFile(p, sysoFile(sizes, pngs), 0o644))
	}
}
