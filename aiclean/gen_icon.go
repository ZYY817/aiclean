//go:build ignore

// gen_icon.go 生成 assets/icon.ico（多尺寸 PNG 内嵌的 ICO）：
// 图案 = 应用内 .lg 的放大版：2×2 圆角方块，左上/右下实心墨色，右上主题蓝，左下描边。
// 用法：go run gen_icon.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

var (
	ink = color.NRGBA{0x1e, 0x23, 0x30, 0xff}
	acc = color.NRGBA{0x4f, 0x5d, 0xf5, 0xff} // 主题靛蓝
)

// rounded rect mask：返回 (x,y) 是否在圆角矩形内
func inRR(px, py float64, x, y, w, h, r float64) bool {
	if px < x || px >= x+w || py < y || py >= y+h {
		return false
	}
	cx := px
	if cx < x+r {
		cx = x + r
	}
	if cx >= x+w-r {
		cx = x + w - r
	}
	cy := py
	if cy < y+r {
		cy = y + r
	}
	if cy >= y+h-r {
		cy = y + h - r
	}
	dx, dy := px-cx, py-cy
	return dx*dx+dy*dy <= r*r+0.5
}

func render(size int) *image.NRGBA {
	s := float64(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	// 2×2 方块：边长 0.40s，间距 0.08s，整体居中
	w := s * 0.40
	gap := s * 0.08
	r := s * 0.10
	off := (s - 2*w - gap) / 2
	type cell struct {
		x, y    float64
		mode    int // 0 实心 ink, 1 实心 acc, 2 描边 ink
	}
	cells := []cell{
		{off, off, 0},
		{off + w + gap, off, 1},
		{off, off + w + gap, 2},
		{off + w + gap, off + w + gap, 0},
	}
	border := s * 0.045
	if border < 1 {
		border = 1
	}
	for _, c := range cells {
		for py := 0; py < size; py++ {
			for px := 0; px < size; px++ {
				fx, fy := float64(px)+0.5, float64(py)+0.5
				switch c.mode {
				case 0:
					if inRR(fx, fy, c.x, c.y, w, w, r) {
						img.SetNRGBA(px, py, ink)
					}
				case 1:
					if inRR(fx, fy, c.x, c.y, w, w, r) {
						img.SetNRGBA(px, py, acc)
					}
				case 2:
					outer := inRR(fx, fy, c.x, c.y, w, w, r)
					inner := inRR(fx, fy, c.x+border, c.y+border, w-2*border, w-2*border, r*0.6)
					if outer && !inner {
						img.SetNRGBA(px, py, ink)
					}
				}
			}
		}
	}
	return img
}

func main() {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, sz := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(sz)); err != nil {
			panic(err)
		}
		pngs = append(pngs, buf.Bytes())
	}
	var out bytes.Buffer
	// ICONDIR
	binary.Write(&out, binary.LittleEndian, uint16(0))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))
	off := 6 + 16*len(sizes)
	for i, sz := range sizes {
		b := byte(sz)
		if sz >= 256 {
			b = 0
		}
		out.WriteByte(b) // width
		out.WriteByte(b) // height
		out.WriteByte(0) // colors
		out.WriteByte(0) // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))     // planes
		binary.Write(&out, binary.LittleEndian, uint16(32))    // bpp
		binary.Write(&out, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(&out, binary.LittleEndian, uint32(off))
		off += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	if err := os.WriteFile("assets/icon.ico", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
	println("icon.ico:", len(out.Bytes()), "bytes,", len(sizes), "sizes")
}
