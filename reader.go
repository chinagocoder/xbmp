package xbmp

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
)

const (
	bmpFileHeaderSize = 14
	coreHeaderSize    = 12 // BITMAPCOREHEADER
	infoHeaderSize    = 40 // BITMAPINFOHEADER
	biRGB             = 0
	biBitFields       = 3
)

type FileHeader struct {
	Signature  [2]byte
	FileSize   uint32
	Reserved   uint32
	DataOffset uint32
}

type InfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
	RedMask       uint32
	GreenMask     uint32
	BlueMask      uint32
	AlphaMask     uint32
}

func Decode(r io.Reader) (image.Image, error) {
	var fileHeader FileHeader
	if err := binary.Read(r, binary.LittleEndian, &fileHeader); err != nil {
		return nil, err
	}
	if string(fileHeader.Signature[:]) != "BM" {
		return nil, errors.New("invalid BMP signature")
	}

	var headerSize uint32
	if err := binary.Read(r, binary.LittleEndian, &headerSize); err != nil {
		return nil, err
	}

	var info InfoHeader
	info.Size = headerSize

	switch headerSize {
	case coreHeaderSize:
		var core struct {
			Width    uint16
			Height   uint16
			Planes   uint16
			BitCount uint16
		}
		if err := binary.Read(r, binary.LittleEndian, &core); err != nil {
			return nil, err
		}
		info.Width = int32(core.Width)
		info.Height = int32(core.Height)
		info.Planes = core.Planes
		info.BitCount = core.BitCount
		info.Compression = biRGB
	case infoHeaderSize:
		remainingHeader := make([]byte, infoHeaderSize-4)
		if _, err := io.ReadFull(r, remainingHeader); err != nil {
			return nil, err
		}
		info.Width = int32(binary.LittleEndian.Uint32(remainingHeader[0:4]))
		info.Height = int32(binary.LittleEndian.Uint32(remainingHeader[4:8]))
		info.Planes = binary.LittleEndian.Uint16(remainingHeader[8:10])
		info.BitCount = binary.LittleEndian.Uint16(remainingHeader[10:12])
		info.Compression = binary.LittleEndian.Uint32(remainingHeader[12:16])
	default:
		return nil, errors.New("unsupported BMP header")
	}

	// 处理调色板
	palette, err := parsePalette(r, info)
	if err != nil {
		return nil, err
	}

	// 处理位掩码
	if info.Compression == biBitFields {
		// 读取颜色掩码
		masks := make([]uint32, 4)
		if err := binary.Read(r, binary.LittleEndian, &masks); err != nil {
			return nil, err
		}
		info.RedMask, info.GreenMask, info.BlueMask, info.AlphaMask = masks[0], masks[1], masks[2], masks[3]
	}

	// 跳转到像素数据
	if _, err := r.(io.Seeker).Seek(int64(fileHeader.DataOffset), io.SeekStart); err != nil {
		return nil, err
	}

	// 创建图像
	return decodePixelData(r, info, palette)
}

func parsePalette(r io.Reader, info InfoHeader) ([]color.Color, error) {
	if info.BitCount > 8 && info.ClrUsed == 0 {
		return nil, nil // 无调色板
	}

	entries := 1 << info.BitCount
	if info.ClrUsed > 0 {
		entries = int(info.ClrUsed)
	}

	palette := make([]color.Color, entries)
	for i := 0; i < entries; i++ {
		var bgr [4]byte
		if _, err := io.ReadFull(r, bgr[:]); err != nil {
			return nil, err
		}
		palette[i] = color.RGBA{bgr[2], bgr[1], bgr[0], 255}
	}
	return palette, nil
}

func decodePixelData(r io.Reader, info InfoHeader, palette []color.Color) (image.Image, error) {
	width := int(info.Width)
	height := int(info.Height)
	if height < 0 {
		height = -height // 处理自顶向下图像
	}

	var img image.Image
	rect := image.Rect(0, 0, width, height)

	switch info.BitCount {
	case 1, 4, 8:
		img = image.NewPaletted(rect, palette)
		err := readIndexedData(r, img.(*image.Paletted), info)
		return img, err
	case 16:
		img = image.NewRGBA64(rect)
		return read16BitData(r, img.(*image.RGBA64), info)
	case 24:
		img = image.NewRGBA(rect)
		return read24BitData(r, img.(*image.RGBA), info)
	case 32:
		img = image.NewRGBA(rect)
		return read32BitData(r, img.(*image.RGBA), info)
	default:
		return nil, errors.New("unsupported bit depth")
	}
}

// readIndexedData 处理 1/4/8 位调色板图像
func readIndexedData(r io.Reader, img *image.Paletted, info InfoHeader) error {
	width := int(info.Width)
	height := int(info.Height)
	if height < 0 {
		height = -height
	}

	bitsPerPixel := int(info.BitCount)
	pixelsPerByte := 8 / bitsPerPixel
	bitMask := byte(1<<bitsPerPixel - 1)

	// 计算每行字节数（4字节对齐）
	bytesPerRow := (width*bitsPerPixel + 31) / 32 * 4
	row := make([]byte, bytesPerRow)

	for y := 0; y < height; y++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return err
		}

		// BMP 行存储为从下到上
		targetY := y
		if info.Height > 0 {
			targetY = height - 1 - y
		}

		for x := 0; x < width; x++ {
			// 计算字节位置和位偏移
			bytePos := x / pixelsPerByte
			bitOffset := uint((x % pixelsPerByte) * bitsPerPixel)
			if info.BitCount == 1 {
				bitOffset = 7 - bitOffset // 1-bit 高位在前
			}

			// 提取颜色索引
			b := row[bytePos]
			idx := (b >> bitOffset) & bitMask
			img.SetColorIndex(x, targetY, idx)
		}
	}
	return nil
}

// read16BitData 处理 16 位色深（支持 RGB555/RGB565/BITFIELDS）
func read16BitData(r io.Reader, img *image.RGBA64, info InfoHeader) (image.Image, error) {
	width := int(info.Width)
	height := int(info.Height)
	if height < 0 {
		height = -height
	}

	// 默认掩码（RGB565）
	redMask := uint32(0xF800)
	greenMask := uint32(0x07E0)
	blueMask := uint32(0x001F)
	if info.Compression == biBitFields {
		redMask = info.RedMask
		greenMask = info.GreenMask
		blueMask = info.BlueMask
	}

	// 计算位掩码参数
	rShift, rBits := maskShift(redMask)
	gShift, gBits := maskShift(greenMask)
	bShift, bBits := maskShift(blueMask)

	bytesPerRow := (width*2 + 3) &^ 3 // 每行对齐到4字节
	row := make([]byte, bytesPerRow)

	for y := 0; y < height; y++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return nil, err
		}

		targetY := y
		if info.Height > 0 {
			targetY = height - 1 - y
		}

		for x := 0; x < width; x++ {
			// 读取16位值（小端序）
			pixel := binary.LittleEndian.Uint16(row[x*2 : x*2+2])

			// 提取颜色分量
			r := uint32(pixel) & redMask
			r = (r >> rShift) << (16 - rBits) // 扩展到16位
			r |= r >> rBits

			g := uint32(pixel) & greenMask
			g = (g >> gShift) << (16 - gBits)
			g |= g >> gBits

			b := uint32(pixel) & blueMask
			b = (b >> bShift) << (16 - bBits)
			b |= b >> bBits

			img.SetRGBA64(x, targetY, color.RGBA64{
				R: uint16(r),
				G: uint16(g),
				B: uint16(b),
				A: 0xFFFF,
			})
		}
	}
	return img, nil
}

// read32BitData 处理 32 位色深（支持 RGBA/BITFIELDS）
func read32BitData(r io.Reader, img *image.RGBA, info InfoHeader) (image.Image, error) {
	width := int(info.Width)
	height := int(info.Height)
	if height < 0 {
		height = -height
	}

	// 默认掩码（标准32位BGRA）
	redMask := uint32(0x00FF0000)
	greenMask := uint32(0x0000FF00)
	blueMask := uint32(0x000000FF)
	alphaMask := uint32(0xFF000000)
	if info.Compression == biBitFields {
		redMask = info.RedMask
		greenMask = info.GreenMask
		blueMask = info.BlueMask
		alphaMask = info.AlphaMask
	}

	// 计算位掩码参数
	rShift, _ := maskShift(redMask)
	gShift, _ := maskShift(greenMask)
	bShift, _ := maskShift(blueMask)
	aShift, _ := maskShift(alphaMask)

	bytesPerRow := width * 4
	row := make([]byte, bytesPerRow)

	for y := 0; y < height; y++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return nil, err
		}

		targetY := y
		if info.Height > 0 {
			targetY = height - 1 - y
		}

		for x := 0; x < width; x++ {
			pixel := binary.LittleEndian.Uint32(row[x*4 : x*4+4])

			r := byte((pixel & redMask) >> rShift)
			g := byte((pixel & greenMask) >> gShift)
			b := byte((pixel & blueMask) >> bShift)
			a := byte((pixel & alphaMask) >> aShift)

			// 如果无Alpha通道，设为255
			if alphaMask == 0 {
				a = 0xFF
			}

			img.Set(x, targetY, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}
	return img, nil
}

// maskShift 计算掩码的位移和有效位数
func maskShift(mask uint32) (shift, bits int) {
	if mask == 0 {
		return 0, 0
	}
	// 计算最低有效位位置
	for (mask&1) == 0 && mask != 0 {
		shift++
		mask >>= 1
	}
	// 计算连续1的位数
	for (mask & 1) == 1 {
		bits++
		mask >>= 1
	}
	return
}

// 示例：读取24位色深数据
func read24BitData(r io.Reader, img *image.RGBA, info InfoHeader) (image.Image, error) {
	bytesPerRow := (int(info.Width)*3 + 3) &^ 3
	row := make([]byte, bytesPerRow)

	for y := 0; y < int(info.Height); y++ {
		if _, err := io.ReadFull(r, row); err != nil {
			return nil, err
		}

		targetY := y
		if info.Height > 0 {
			targetY = int(info.Height) - 1 - y // 倒序
		}

		for x := 0; x < int(info.Width); x++ {
			offset := x * 3
			img.Set(x, targetY, color.RGBA{
				R: row[offset+2],
				G: row[offset+1],
				B: row[offset],
				A: 255,
			})
		}
	}
	return img, nil
}
