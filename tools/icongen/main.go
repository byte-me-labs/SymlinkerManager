// Command icongen draws SymlinkerManager's application icon and writes the
// Windows .ico, a 256px PNG for the window/taskbar, and a preview sheet.
//
//	go run ./tools/icongen
//
// The icon is a blue folder with a white "redirect" badge: the app replaces a
// system-drive directory with a junction that points at the data disk.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/vector"
)

const canvas = 1024

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 96, 128, 256}

func main() {
	outDir := "assets"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatal(err)
	}

	rendered := make(map[int]*image.RGBA, len(sizes))
	for _, size := range sizes {
		rendered[size] = drawIcon(size)
	}

	for _, size := range sizes {
		if err := writePNG(filepath.Join(outDir, fmt.Sprintf("icon-%d.png", size)), rendered[size]); err != nil {
			fatal(err)
		}
	}
	if err := writePNG(filepath.Join(outDir, "icon.png"), rendered[256]); err != nil {
		fatal(err)
	}
	if err := writeICO(filepath.Join(outDir, "app.ico"), rendered); err != nil {
		fatal(err)
	}
	if err := writePNG(".dev/icon-preview.png", preview(rendered)); err != nil {
		fatal(err)
	}
	fmt.Println("wrote assets/app.ico, assets/icon.png and .dev/icon-preview.png")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// ----- drawing -----

func drawIcon(size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float32(size) / canvas
	px := func(v float32) float32 { return v * s }

	// Folder silhouette as one closed path: unioning two rounded rectangles
	// leaves an antialiased seam where the tab meets the body.
	folder := pathMask(size, func(r *vector.Rasterizer) {
		folderPath(r, px(104), px(188), px(470), px(268), px(920), px(852), px(38), px(70))
	})
	grad := gradient(size, color.NRGBA{0x63, 0x9B, 0xFF, 0xFF}, color.NRGBA{0x1B, 0x4F, 0xC8, 0xFF}, 0, size)
	draw.DrawMask(dst, dst.Bounds(), grad, image.Point{}, folder, image.Point{}, draw.Over)

	// A soft top highlight gives the folder a little depth.
	highlight := gradient(size,
		color.NRGBA{0xFF, 0xFF, 0xFF, 0x2E},
		color.NRGBA{0xFF, 0xFF, 0xFF, 0x00},
		int(px(268)), int(px(480)))
	draw.DrawMask(dst, dst.Bounds(), highlight, image.Point{}, folder, image.Point{}, draw.Over)

	if size <= 32 {
		// At 16–32 px the badge would be a dot: put a larger white arrow
		// directly on the folder instead.
		arrow := pathMask(size, func(r *vector.Rasterizer) {
			arrowPath(r, px(280), px(560), px(520), px(58), px(155))
		})
		white := image.NewUniform(color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF})
		draw.DrawMask(dst, dst.Bounds(), white, image.Point{}, arrow, image.Point{}, draw.Over)
		return dst
	}

	// Circular badge at the folder's bottom-right corner. A dark blue rim
	// keeps the white face separate from a white desktop.
	rim := circleMask(size, px(758), px(758), px(184))
	draw.DrawMask(dst, dst.Bounds(), image.NewUniform(color.NRGBA{0x17, 0x3F, 0x96, 0xFF}),
		image.Point{}, rim, image.Point{}, draw.Over)
	badge := circleMask(size, px(758), px(758), px(174))
	draw.DrawMask(dst, dst.Bounds(), image.NewUniform(color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}),
		image.Point{}, badge, image.Point{}, draw.Over)

	arrow := pathMask(size, func(r *vector.Rasterizer) {
		arrowPath(r, px(668), px(758), px(194), px(30), px(88))
	})
	blue := image.NewUniform(color.NRGBA{0x1B, 0x4F, 0xC8, 0xFF})
	draw.DrawMask(dst, dst.Bounds(), blue, image.Point{}, arrow, image.Point{}, draw.Over)
	return dst
}

// arrowPath adds a right-pointing arrow whose shaft starts at x0 and whose
// head ends at x0+span. half is the shaft's half thickness; headHalf is the
// head's half height.
func arrowPath(r *vector.Rasterizer, x0, cy, span, half, headHalf float32) {
	headStart := x0 + span*0.62
	tip := x0 + span
	r.MoveTo(x0, cy-half)
	r.LineTo(headStart, cy-half)
	r.LineTo(headStart, cy-headHalf)
	r.LineTo(tip, cy)
	r.LineTo(headStart, cy+headHalf)
	r.LineTo(headStart, cy+half)
	r.LineTo(x0, cy+half)
	r.ClosePath()
}

// folderPath is one folder silhouette: a tab from (x0,y0) to (tabX,tabY)
// above a body from (x0,tabY) to (x1,y1).
func folderPath(r *vector.Rasterizer, x0, y0, tabX, tabY, x1, y1, tabRadius, bodyRadius float32) {
	kt := tabRadius * 0.5522847498
	kb := bodyRadius * 0.5522847498

	r.MoveTo(x0+tabRadius, y0)
	r.LineTo(tabX-tabRadius, y0)
	r.CubeTo(tabX-tabRadius+kt, y0, tabX, y0+tabRadius-kt, tabX, y0+tabRadius)
	r.LineTo(tabX, tabY)
	r.LineTo(x1-bodyRadius, tabY)
	r.CubeTo(x1-bodyRadius+kb, tabY, x1, tabY+bodyRadius-kb, x1, tabY+bodyRadius)
	r.LineTo(x1, y1-bodyRadius)
	r.CubeTo(x1, y1-bodyRadius+kb, x1-bodyRadius+kb, y1, x1-bodyRadius, y1)
	r.LineTo(x0+bodyRadius, y1)
	r.CubeTo(x0+bodyRadius-kb, y1, x0, y1-bodyRadius+kb, x0, y1-bodyRadius)
	r.LineTo(x0, y0+tabRadius)
	r.CubeTo(x0, y0+tabRadius-kt, x0+tabRadius-kt, y0, x0+tabRadius, y0)
	r.ClosePath()
}

func circleMask(size int, cx, cy, radius float32) *image.Alpha {
	return pathMask(size, func(r *vector.Rasterizer) {
		k := radius * 0.5522847498
		r.MoveTo(cx+radius, cy)
		r.CubeTo(cx+radius, cy+k, cx+k, cy+radius, cx, cy+radius)
		r.CubeTo(cx-k, cy+radius, cx-radius, cy+k, cx-radius, cy)
		r.CubeTo(cx-radius, cy-k, cx-k, cy-radius, cx, cy-radius)
		r.CubeTo(cx+k, cy-radius, cx+radius, cy-k, cx+radius, cy)
		r.ClosePath()
	})
}

func pathMask(size int, build func(*vector.Rasterizer)) *image.Alpha {
	r := vector.NewRasterizer(size, size)
	r.DrawOp = draw.Src
	build(r)
	mask := image.NewAlpha(image.Rect(0, 0, size, size))
	r.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	return mask
}

// gradient paints c0 to c1 vertically between y0 and y1, transparent outside.
// It returns NRGBA so that partially transparent white is not mistaken for a
// premultiplied RGBA value.
func gradient(size int, c0, c1 color.NRGBA, y0, y1 int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	if y1 <= y0 {
		y1 = y0 + 1
	}
	for y := 0; y < size; y++ {
		t := float64(y-y0) / float64(y1-y0)
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		c := color.NRGBA{
			R: uint8(float64(c0.R) + (float64(c1.R)-float64(c0.R))*t),
			G: uint8(float64(c0.G) + (float64(c1.G)-float64(c0.G))*t),
			B: uint8(float64(c0.B) + (float64(c1.B)-float64(c0.B))*t),
			A: uint8(float64(c0.A) + (float64(c1.A)-float64(c0.A))*t),
		}
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// ----- encoding -----

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeICO(path string, imgs map[int]*image.RGBA) error {
	type entry struct {
		size int
		data []byte
	}
	entries := make([]entry, 0, len(sizes))
	for _, size := range sizes {
		img := imgs[size]
		var data []byte
		var err error
		if size >= 128 {
			var buf bytes.Buffer
			err = png.Encode(&buf, img)
			data = buf.Bytes()
		} else {
			data = encodeDIB(img)
		}
		if err != nil {
			return err
		}
		entries = append(entries, entry{size: size, data: data})
	}

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(entries)))

	offset := 6 + 16*len(entries)
	for _, e := range entries {
		w := byte(e.size)
		if e.size == 256 {
			w = 0
		}
		buf.WriteByte(w)
		buf.WriteByte(w)
		buf.WriteByte(0)                                    // palette
		buf.WriteByte(0)                                    // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(e.data)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(e.data)
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// encodeDIB writes a 32-bit BGRA bottom-up DIB with an empty AND mask, the
// traditional payload for icon sizes up to 64 px.
func encodeDIB(img *image.RGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	maskStride := ((w + 31) / 32) * 4
	size := 40 + w*h*4 + maskStride*h
	buf := make([]byte, size)

	le := binary.LittleEndian
	le.PutUint32(buf[0:], 40)             // header size
	le.PutUint32(buf[4:], uint32(w))      // width
	le.PutUint32(buf[8:], uint32(h*2))    // height: XOR + AND
	le.PutUint16(buf[12:], 1)             // planes
	le.PutUint16(buf[14:], 32)            // bits per pixel
	le.PutUint32(buf[16:], 0)             // BI_RGB
	le.PutUint32(buf[20:], uint32(w*h*4)) // image size

	off := 40
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			buf[off+0] = c.B
			buf[off+1] = c.G
			buf[off+2] = c.R
			buf[off+3] = c.A
			off += 4
		}
	}
	return buf
}

// ----- preview -----

func preview(imgs map[int]*image.RGBA) *image.RGBA {
	const w, h = 980, 420
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	// Light panel.
	draw.Draw(out, image.Rect(0, 0, w, h/2), image.NewUniform(color.NRGBA{0xF3, 0xF4, 0xF6, 0xFF}), image.Point{}, draw.Src)
	drawIconSheet(out, imgs, 20, 20)
	// Dark panel.
	draw.Draw(out, image.Rect(0, h/2, w, h), image.NewUniform(color.NRGBA{0x1F, 0x29, 0x37, 0xFF}), image.Point{}, draw.Src)
	drawIconSheet(out, imgs, 20, h/2+20)
	return out
}

func drawIconSheet(out *image.RGBA, imgs map[int]*image.RGBA, x0, y0 int) {
	x := x0
	for _, size := range []int{16, 24, 32, 48, 64} {
		scale := 3
		if size >= 64 {
			scale = 2
		}
		scaled := scaleNearest(imgs[size], scale)
		draw.Draw(out, image.Rect(x, y0, x+scaled.Bounds().Dx(), y0+scaled.Bounds().Dy()), scaled, image.Point{}, draw.Over)
		x += scaled.Bounds().Dx() + 24
	}
	large := imgs[256]
	draw.Draw(out, image.Rect(x, y0, x+256, y0+256), large, image.Point{}, draw.Over)
}

func scaleNearest(src *image.RGBA, n int) *image.RGBA {
	w, h := src.Bounds().Dx()*n, src.Bounds().Dy()*n
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.SetRGBA(x, y, src.RGBAAt(x/n, y/n))
		}
	}
	return dst
}
