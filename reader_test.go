package bmp

import (
	"image/color"
	"os"
	"testing"
)

// when there are no headers or data is empty
func TestDecode(t *testing.T) {
	file, _ := os.Open("data/sign.bmp")
	defer file.Close()

	img, err := Decode(file)
	if err != nil {
		t.Fatal(err)
	}

	// 检查中心像素颜色
	c := img.At(1, 1).(color.RGBA)
	if c.R != 0x1 || c.G != 0x1 || c.B != 0x1 {
		t.Errorf("颜色值不符合预期: %04X %04X %04X", c.R, c.G, c.B)
	}
}
