package interp

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

const (
	imageGIF    = 1
	imageJPEG   = 2
	imagePNG    = 3
	imageSWF    = 4
	imagePSD    = 5
	imageBMP    = 6
	imageTIFFII = 7
	imageTIFFMM = 8
	imageJPC    = 9
	imageJP2    = 10
	imageSWC    = 13
	imageIFF    = 14
	imageWBMP   = 15
	imageXBM    = 16
	imageICO    = 17
	imageWEBP   = 18
	imageAVIF   = 19
)

type imageDimensions struct {
	width, height  uint32
	bits, channels uint32
}

type imageParseResult struct {
	dimensions imageDimensions
	typeID     int
	jpegAPP    map[string]string
}

func imageMIME(typeID int) string {
	switch typeID {
	case imageGIF:
		return "image/gif"
	case imageJPEG:
		return "image/jpeg"
	case imagePNG:
		return "image/png"
	case imageSWF, imageSWC:
		return "application/x-shockwave-flash"
	case imagePSD:
		return "image/psd"
	case imageBMP:
		return "image/bmp"
	case imageTIFFII, imageTIFFMM:
		return "image/tiff"
	case imageJPC:
		return "application/octet-stream"
	case imageJP2:
		return "image/jp2"
	case imageIFF:
		return "image/iff"
	case imageWBMP:
		return "image/vnd.wap.wbmp"
	case imageXBM:
		return "image/xbm"
	case imageICO:
		return "image/vnd.microsoft.icon"
	case imageWEBP:
		return "image/webp"
	case imageAVIF:
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}

func imageResultValue(result imageParseResult) php.Value {
	dim := result.dimensions
	array := php.NewArray()
	array.Append(php.Int(int64(dim.width)))
	array.Append(php.Int(int64(dim.height)))
	array.Append(php.Int(int64(result.typeID)))
	array.Append(php.String(fmt.Sprintf(`width="%d" height="%d"`, dim.width, dim.height)))
	if dim.bits != 0 {
		array.Set(php.StringKey("bits"), php.Int(int64(dim.bits)))
	}
	if dim.channels != 0 {
		array.Set(php.StringKey("channels"), php.Int(int64(dim.channels)))
	}
	array.Set(php.StringKey("mime"), php.String(imageMIME(result.typeID)))
	return php.Arr(array)
}

func (in *Interp) getImageSize(ctx *php.Ctx, name string, args []php.Value) (php.Value, error) {
	input := args[0].Deref().String()
	if name == "getimagesize" && strings.IndexByte(input, 0) >= 0 {
		return php.Undef(), Raise("ValueError",
			"getimagesize(): Argument #1 ($filename) must not contain any null bytes")
	}

	var metadata *php.Array
	if len(args) == 2 {
		metadata = php.NewArray()
		if ref, ok := args[1].RefOf(); ok {
			ref.Set(php.Arr(metadata))
		}
	}

	data := []byte(input)
	if name == "getimagesize" {
		var err error
		data, err = in.readFile(ctx, input)
		if err != nil {
			ctx.Warn("getimagesize(%s): Failed to open stream: No such file or directory", input)
			return php.Bool(false), nil
		}
	}
	result, ok := parseImageSize(ctx, name, data, input, metadata != nil)
	if !ok {
		return php.Bool(false), nil
	}
	if metadata != nil {
		for key, value := range result.jpegAPP {
			metadata.Set(php.StringKey(key), php.String(value))
		}
	}
	return imageResultValue(result), nil
}

func parseImageSize(ctx *php.Ctx, name string, data []byte, input string, wantAPP bool) (imageParseResult, bool) {
	noticeRead := func() {
		ctx.Notice("%s(): Error reading from %s!", name, input)
	}
	if len(data) < 3 {
		noticeRead()
		return imageParseResult{}, false
	}

	if string(data[:3]) == "GIF" {
		if len(data) < 11 {
			return imageParseResult{}, false
		}
		bits := uint32(0)
		if data[10]&0x80 != 0 {
			bits = uint32(data[10]&7) + 1
		}
		return imageParseResult{typeID: imageGIF, dimensions: imageDimensions{
			width: uint32(binary.LittleEndian.Uint16(data[6:8])), height: uint32(binary.LittleEndian.Uint16(data[8:10])),
			bits: bits, channels: 3,
		}}, true
	}
	if data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return parseJPEG(ctx, name, data, wantAPP)
	}
	if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' {
		if len(data) < 8 {
			noticeRead()
			return imageParseResult{}, false
		}
		if string(data[:8]) != "\x89PNG\r\n\x1a\n" {
			ctx.Warn("%s(): PNG file corrupted by ASCII conversion", name)
			return imageParseResult{}, false
		}
		if len(data) < 25 {
			return imageParseResult{}, false
		}
		return imageParseResult{typeID: imagePNG, dimensions: imageDimensions{
			width: binary.BigEndian.Uint32(data[16:20]), height: binary.BigEndian.Uint32(data[20:24]), bits: uint32(data[24]),
		}}, true
	}
	if string(data[:3]) == "FWS" {
		if dim, ok := parseSWFRect(data, 8); ok {
			return imageParseResult{typeID: imageSWF, dimensions: dim}, true
		}
		return imageParseResult{}, false
	}
	if string(data[:3]) == "CWS" {
		ctx.Notice("%s(): The image is a compressed SWF file, but you do not have a static version of the zlib extension enabled", name)
		return imageParseResult{}, false
	}
	if string(data[:3]) == "8BP" {
		if len(data) < 22 {
			return imageParseResult{}, false
		}
		return imageParseResult{typeID: imagePSD, dimensions: imageDimensions{
			width: binary.BigEndian.Uint32(data[18:22]), height: binary.BigEndian.Uint32(data[14:18]),
		}}, true
	}
	if data[0] == 'B' && data[1] == 'M' {
		if dim, ok := parseBMP(data); ok {
			return imageParseResult{typeID: imageBMP, dimensions: dim}, true
		}
		return imageParseResult{}, false
	}
	if data[0] == 0xff && data[1] == 0x4f && data[2] == 0xff {
		if dim, ok := parseJPC(data); ok {
			return imageParseResult{typeID: imageJPC, dimensions: dim}, true
		}
		if len(data) > 3 && data[3] != 0x51 {
			ctx.Warn("%s(): JPEG2000 codestream corrupt(Expected SIZ marker not found after SOC)", name)
		}
		return imageParseResult{}, false
	}
	if string(data[:3]) == "RIF" {
		if len(data) < 12 {
			noticeRead()
			return imageParseResult{}, false
		}
		if string(data[8:12]) == "WEBP" {
			if dim, ok := parseWebP(data); ok {
				return imageParseResult{typeID: imageWEBP, dimensions: dim}, true
			}
		}
		return imageParseResult{}, false
	}

	if len(data) < 4 {
		noticeRead()
		return imageParseResult{}, false
	}
	switch string(data[:4]) {
	case "II*\x00":
		if dim, ok := parseTIFF(data, binary.LittleEndian); ok {
			return imageParseResult{typeID: imageTIFFII, dimensions: dim}, true
		}
		return imageParseResult{}, false
	case "MM\x00*":
		if dim, ok := parseTIFF(data, binary.BigEndian); ok {
			return imageParseResult{typeID: imageTIFFMM, dimensions: dim}, true
		}
		return imageParseResult{}, false
	case "FORM":
		if dim, ok := parseIFF(data); ok {
			return imageParseResult{typeID: imageIFF, dimensions: dim}, true
		}
		return imageParseResult{}, false
	case "\x00\x00\x01\x00":
		if dim, ok := parseICO(data); ok {
			return imageParseResult{typeID: imageICO, dimensions: dim}, true
		}
		return imageParseResult{}, false
	}

	twelve := len(data) >= 12
	if twelve && string(data[:12]) == "\x00\x00\x00\x0cjP  \r\n\x87\n" {
		if dim, ok := parseJP2(data); ok {
			return imageParseResult{typeID: imageJP2, dimensions: dim}, true
		}
		ctx.Warn("%s(): JP2 file has no codestreams at root level", name)
		return imageParseResult{}, false
	}
	if dim, ok := parseAVIF(data); ok {
		return imageParseResult{typeID: imageAVIF, dimensions: dim}, true
	}
	if dim, ok := parseWBMP(data); ok {
		return imageParseResult{typeID: imageWBMP, dimensions: dim}, true
	}
	if !twelve {
		noticeRead()
		return imageParseResult{}, false
	}
	if dim, ok := parseXBM(data); ok {
		return imageParseResult{typeID: imageXBM, dimensions: dim}, true
	}
	return imageParseResult{}, false
}

func parseSWFRect(data []byte, offset int) (imageDimensions, bool) {
	// php_handle_swf insists on the same fixed 32-byte read before decoding.
	if offset < 0 || len(data)-offset < 32 {
		return imageDimensions{}, false
	}
	data = data[offset : offset+32]
	bits := uint(bitField(data, 0, 5))
	if bits == 0 || 5+4*bits > uint(len(data)*8) {
		return imageDimensions{}, false
	}
	xmin := bitField(data, 5, bits)
	xmax := bitField(data, 5+bits, bits)
	ymin := bitField(data, 5+2*bits, bits)
	ymax := bitField(data, 5+3*bits, bits)
	return imageDimensions{width: uint32((xmax - xmin) / 20), height: uint32((ymax - ymin) / 20)}, true
}

func bitField(data []byte, position, count uint) uint64 {
	var value uint64
	for i := uint(0); i < count; i++ {
		value = value<<1 | uint64((data[(position+i)/8]>>(7-((position+i)%8)))&1)
	}
	return value
}

func parseBMP(data []byte) (imageDimensions, bool) {
	if len(data) < 30 {
		return imageDimensions{}, false
	}
	size := binary.LittleEndian.Uint32(data[14:18])
	if size == 12 {
		return imageDimensions{
			width: uint32(binary.LittleEndian.Uint16(data[18:20])), height: uint32(binary.LittleEndian.Uint16(data[20:22])), bits: uint32(data[25]),
		}, true
	}
	if !(size > 12 && (size <= 64 || size == 108 || size == 124)) {
		return imageDimensions{}, false
	}
	height := int32(binary.LittleEndian.Uint32(data[22:26]))
	if height < 0 {
		height = -height
	}
	return imageDimensions{
		width: binary.LittleEndian.Uint32(data[18:22]), height: uint32(height), bits: uint32(binary.LittleEndian.Uint16(data[28:30])),
	}, true
}

func parseJPEG(ctx *php.Ctx, name string, data []byte, wantAPP bool) (imageParseResult, bool) {
	result := imageParseResult{typeID: imageJPEG}
	foundSOF := false
	if wantAPP {
		result.jpegAPP = map[string]string{}
	}
	position := 2
	for {
		extraneous := 0
		for position < len(data) && data[position] != 0xff {
			position++
			extraneous++
		}
		if extraneous != 0 {
			ctx.Warn("%s(): Corrupt JPEG Data: %d extraneous bytes before marker", name, extraneous)
		}
		if position >= len(data) {
			return result, foundSOF
		}
		for position < len(data) && data[position] == 0xff {
			position++
		}
		if position >= len(data) {
			return result, foundSOF
		}
		marker := data[position]
		position++
		if marker == 0xd9 || marker == 0xda {
			return result, foundSOF
		}
		if position+2 > len(data) {
			return result, foundSOF
		}
		length := int(binary.BigEndian.Uint16(data[position : position+2]))
		if length < 2 {
			return result, foundSOF
		}
		end := position + length
		if end > len(data) {
			return result, foundSOF
		}
		if marker >= 0xe0 && marker <= 0xef && wantAPP {
			key := fmt.Sprintf("APP%d", marker-0xe0)
			if _, exists := result.jpegAPP[key]; !exists {
				result.jpegAPP[key] = string(data[position+2 : end])
			}
		}
		if isJPEGSOF(marker) && !foundSOF {
			if length < 8 || position+8 > len(data) {
				return result, false
			}
			result.dimensions.bits = uint32(data[position+2])
			result.dimensions.height = uint32(binary.BigEndian.Uint16(data[position+3 : position+5]))
			result.dimensions.width = uint32(binary.BigEndian.Uint16(data[position+5 : position+7]))
			result.dimensions.channels = uint32(data[position+7])
			foundSOF = true
			if !wantAPP {
				return result, true
			}
		}
		position = end
	}
}

func isJPEGSOF(marker byte) bool {
	switch marker {
	case 0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf:
		return true
	default:
		return false
	}
}

func parseJPC(data []byte) (imageDimensions, bool) {
	if len(data) < 42 || data[0] != 0xff || data[1] != 0x4f || data[2] != 0xff || data[3] != 0x51 {
		return imageDimensions{}, false
	}
	channels := uint32(binary.BigEndian.Uint16(data[40:42]))
	if channels == 0 || channels > 256 || len(data) < 42+int(channels)*3 {
		return imageDimensions{}, false
	}
	bits := uint32(0)
	for i := 0; i < int(channels); i++ {
		depth := uint32(data[42+i*3]) + 1
		if depth > bits {
			bits = depth
		}
	}
	return imageDimensions{
		width: binary.BigEndian.Uint32(data[8:12]), height: binary.BigEndian.Uint32(data[12:16]), bits: bits, channels: channels,
	}, true
}

func parseJP2(data []byte) (imageDimensions, bool) {
	for position := 12; position+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[position : position+4]))
		header := uint64(8)
		if size == 1 {
			if position+16 > len(data) {
				return imageDimensions{}, false
			}
			size = binary.BigEndian.Uint64(data[position+8 : position+16])
			header = 16
		} else if size == 0 {
			size = uint64(len(data) - position)
		}
		if size < header || size > uint64(len(data)-position) {
			return imageDimensions{}, false
		}
		if string(data[position+4:position+8]) == "jp2c" {
			return parseJPC(data[position+int(header) : position+int(size)])
		}
		position += int(size)
	}
	return imageDimensions{}, false
}

func parseTIFF(data []byte, order binary.ByteOrder) (imageDimensions, bool) {
	if len(data) < 8 {
		return imageDimensions{}, false
	}
	offset := uint64(order.Uint32(data[4:8]))
	if offset+2 > uint64(len(data)) {
		return imageDimensions{}, false
	}
	count := uint64(order.Uint16(data[offset : offset+2]))
	if count > (math.MaxUint64-offset-6)/12 || offset+2+count*12+4 > uint64(len(data)) {
		return imageDimensions{}, false
	}
	var width, height uint32
	for i := uint64(0); i < count; i++ {
		entry := data[offset+2+i*12 : offset+14+i*12]
		tag, format := order.Uint16(entry[:2]), order.Uint16(entry[2:4])
		var value uint32
		switch format {
		case 1, 6:
			value = uint32(entry[8])
		case 3, 8:
			value = uint32(order.Uint16(entry[8:10]))
		case 4, 9:
			value = order.Uint32(entry[8:12])
		default:
			continue
		}
		switch tag {
		case 0x100, 0xa002:
			width = value
		case 0x101, 0xa003:
			height = value
		}
	}
	return imageDimensions{width: width, height: height}, width != 0 && height != 0
}

func parseIFF(data []byte) (imageDimensions, bool) {
	if len(data) < 12 || (string(data[8:12]) != "ILBM" && string(data[8:12]) != "PBM ") {
		return imageDimensions{}, false
	}
	for position := 12; position+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[position+4 : position+8]))
		padded := size + size%2
		if padded > uint64(len(data)-position-8) {
			return imageDimensions{}, false
		}
		if string(data[position:position+4]) == "BMHD" {
			if size < 9 {
				return imageDimensions{}, false
			}
			width := int16(binary.BigEndian.Uint16(data[position+8 : position+10]))
			height := int16(binary.BigEndian.Uint16(data[position+10 : position+12]))
			bits := data[position+16]
			if width > 0 && height > 0 && bits > 0 && bits < 33 {
				return imageDimensions{width: uint32(width), height: uint32(height), bits: uint32(bits)}, true
			}
		}
		position += 8 + int(padded)
	}
	return imageDimensions{}, false
}

func parseWBMP(data []byte) (imageDimensions, bool) {
	if len(data) == 0 || data[0] != 0 {
		return imageDimensions{}, false
	}
	position := 1
	readMulti := func(limit uint32) (uint32, bool) {
		var value uint32
		for {
			if position >= len(data) {
				return 0, false
			}
			b := data[position]
			position++
			value = value<<7 | uint32(b&0x7f)
			if value > limit {
				return 0, false
			}
			if b&0x80 == 0 {
				return value, true
			}
		}
	}
	if _, ok := readMulti(math.MaxUint32); !ok {
		return imageDimensions{}, false
	}
	width, ok := readMulti(2048)
	if !ok {
		return imageDimensions{}, false
	}
	height, ok := readMulti(2048)
	return imageDimensions{width: width, height: height}, ok && width != 0 && height != 0
}

func parseXBM(data []byte) (imageDimensions, bool) {
	var width, height uint32
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "#define" {
			continue
		}
		value, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			continue
		}
		kind := fields[1]
		if at := strings.LastIndexByte(kind, '_'); at >= 0 {
			kind = kind[at+1:]
		}
		switch kind {
		case "width":
			width = uint32(value)
		case "height":
			height = uint32(value)
		}
		if width != 0 && height != 0 {
			return imageDimensions{width: width, height: height}, true
		}
	}
	return imageDimensions{}, false
}

func parseICO(data []byte) (imageDimensions, bool) {
	if len(data) < 6 {
		return imageDimensions{}, false
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count < 1 || count > 255 {
		return imageDimensions{}, false
	}
	var result imageDimensions
	position := 6
	for ; count > 0 && position+16 <= len(data); count-- {
		entry := data[position : position+16]
		bits := uint32(binary.LittleEndian.Uint16(entry[6:8]))
		if bits >= result.bits {
			result.width, result.height, result.bits = uint32(entry[0]), uint32(entry[1]), bits
		}
		position += 16
	}
	if result.width == 0 {
		result.width = 256
	}
	if result.height == 0 {
		result.height = 256
	}
	return result, true
}

func parseWebP(data []byte) (imageDimensions, bool) {
	if len(data) < 30 || string(data[12:15]) != "VP8" {
		return imageDimensions{}, false
	}
	buf := data[12:30]
	result := imageDimensions{bits: 8}
	switch buf[3] {
	case ' ':
		result.width = uint32(buf[14]) + uint32(buf[15]&0x3f)<<8
		result.height = uint32(buf[16]) + uint32(buf[17]&0x3f)<<8
	case 'L':
		result.width = uint32(buf[9]) + uint32(buf[10]&0x3f)<<8 + 1
		result.height = uint32(buf[10]>>6) + uint32(buf[11])<<2 + uint32(buf[12]&0xf)<<10 + 1
	case 'X':
		result.width = uint32(buf[12]) + uint32(buf[13])<<8 + uint32(buf[14])<<16 + 1
		result.height = uint32(buf[15]) + uint32(buf[16])<<8 + uint32(buf[17])<<16 + 1
	default:
		return imageDimensions{}, false
	}
	return result, true
}

type imageBox struct {
	typeName string
	body     []byte
	version  byte
	flags    uint32
}

func parseImageBoxes(data []byte, full map[string]bool) ([]imageBox, bool) {
	var boxes []imageBox
	for len(data) != 0 {
		if len(data) < 8 {
			return nil, false
		}
		size, header := uint64(binary.BigEndian.Uint32(data[:4])), uint64(8)
		if size == 1 {
			if len(data) < 16 {
				return nil, false
			}
			size, header = binary.BigEndian.Uint64(data[8:16]), 16
		} else if size == 0 {
			size = uint64(len(data))
		}
		if size < header || size > uint64(len(data)) {
			return nil, false
		}
		box := imageBox{typeName: string(data[4:8]), body: data[header:size]}
		if full[box.typeName] {
			if len(box.body) < 4 {
				return nil, false
			}
			box.version = box.body[0]
			box.flags = uint32(box.body[1])<<16 | uint32(box.body[2])<<8 | uint32(box.body[3])
			box.body = box.body[4:]
		}
		boxes = append(boxes, box)
		data = data[size:]
	}
	return boxes, true
}

func parseAVIF(data []byte) (imageDimensions, bool) {
	top, ok := parseImageBoxes(data, map[string]bool{"meta": true})
	if !ok || len(top) == 0 || top[0].typeName != "ftyp" || len(top[0].body) < 8 {
		return imageDimensions{}, false
	}
	brand := false
	for position := 0; position+4 <= len(top[0].body) && position <= 132; position += 4 {
		if position != 4 && (string(top[0].body[position:position+4]) == "avif" || string(top[0].body[position:position+4]) == "avis") {
			brand = true
			break
		}
	}
	if !brand {
		return imageDimensions{}, false
	}
	var meta *imageBox
	for i := range top {
		if top[i].typeName == "meta" && top[i].version == 0 {
			meta = &top[i]
			break
		}
	}
	if meta == nil {
		return imageDimensions{}, false
	}
	full := map[string]bool{"pitm": true, "iinf": true, "ipma": true, "ispe": true, "pixi": true, "iref": true, "auxC": true}
	children, ok := parseImageBoxes(meta.body, full)
	if !ok {
		return imageDimensions{}, false
	}
	var primary uint32
	iinfSeen := false
	var iprp, iref []byte
	var irefVersion byte
	for _, child := range children {
		switch child.typeName {
		case "pitm":
			if child.version == 0 && len(child.body) >= 2 {
				primary = uint32(binary.BigEndian.Uint16(child.body[:2]))
			} else if child.version == 1 && len(child.body) >= 4 {
				primary = binary.BigEndian.Uint32(child.body[:4])
			}
		case "iinf":
			iinfSeen = child.version <= 1
		case "iref":
			iref, irefVersion = child.body, child.version
		case "iprp":
			iprp = child.body
		}
	}
	if primary == 0 || !iinfSeen || iprp == nil {
		return imageDimensions{}, false
	}
	type property struct{ dim imageDimensions }
	properties := map[uint32]property{}
	associations := map[uint32][]uint32{}
	tiles := map[uint32][]uint32{}
	alpha := false
	if iref != nil {
		references, valid := parseImageBoxes(iref, nil)
		if !valid {
			return imageDimensions{}, false
		}
		idBytes := 2
		if irefVersion != 0 {
			idBytes = 4
		}
		for _, reference := range references {
			if reference.typeName != "dimg" || len(reference.body) < idBytes+2 {
				continue
			}
			var parent uint32
			if idBytes == 2 {
				parent = uint32(binary.BigEndian.Uint16(reference.body[:2]))
			} else {
				parent = binary.BigEndian.Uint32(reference.body[:4])
			}
			count := int(binary.BigEndian.Uint16(reference.body[idBytes : idBytes+2]))
			body := reference.body[idBytes+2:]
			if count > len(body)/idBytes {
				return imageDimensions{}, false
			}
			for index := 0; index < count; index++ {
				var tile uint32
				if idBytes == 2 {
					tile = uint32(binary.BigEndian.Uint16(body[:2]))
				} else {
					tile = binary.BigEndian.Uint32(body[:4])
				}
				tiles[parent] = append(tiles[parent], tile)
				body = body[idBytes:]
			}
		}
	}
	iprpBoxes, ok := parseImageBoxes(iprp, full)
	if !ok {
		return imageDimensions{}, false
	}
	for _, child := range iprpBoxes {
		switch child.typeName {
		case "ipco":
			props, valid := parseImageBoxes(child.body, full)
			if !valid {
				return imageDimensions{}, false
			}
			for index, prop := range props {
				held := properties[uint32(index+1)]
				switch prop.typeName {
				case "ispe":
					if prop.version == 0 && len(prop.body) >= 8 {
						held.dim.width = binary.BigEndian.Uint32(prop.body[:4])
						held.dim.height = binary.BigEndian.Uint32(prop.body[4:8])
					}
				case "pixi":
					if prop.version == 0 && len(prop.body) >= 2 {
						channels := int(prop.body[0])
						if channels > 0 && len(prop.body) >= channels+1 {
							depth := prop.body[1]
							valid := depth != 0
							for _, each := range prop.body[2 : channels+1] {
								valid = valid && each == depth
							}
							if valid {
								held.dim.bits, held.dim.channels = uint32(depth), uint32(channels)
							}
						}
					}
				case "av1C":
					if len(prop.body) >= 3 {
						high, twelve := prop.body[2]&0x40 != 0, prop.body[2]&0x20 != 0
						if !twelve || high {
							held.dim.bits = 8
							if high {
								held.dim.bits = 10
								if twelve {
									held.dim.bits = 12
								}
							}
							held.dim.channels = 3
							if prop.body[2]&0x10 != 0 {
								held.dim.channels = 1
							}
						}
					}
				case "auxC":
					alpha = alpha || strings.HasPrefix(string(prop.body), "urn:mpeg:mpegB:cicp:systems:auxiliary:alpha\x00")
				}
				properties[uint32(index+1)] = held
			}
		case "ipma":
			body := child.body
			if len(body) < 4 {
				return imageDimensions{}, false
			}
			entries := binary.BigEndian.Uint32(body[:4])
			body = body[4:]
			idBytes, indexBytes := 2, 1
			if child.version >= 1 {
				idBytes = 4
			}
			if child.flags&1 != 0 {
				indexBytes = 2
			}
			for entry := uint32(0); entry < entries; entry++ {
				if len(body) < idBytes+1 {
					return imageDimensions{}, false
				}
				var item uint32
				if idBytes == 2 {
					item = uint32(binary.BigEndian.Uint16(body[:2]))
				} else {
					item = binary.BigEndian.Uint32(body[:4])
				}
				count := int(body[idBytes])
				body = body[idBytes+1:]
				for i := 0; i < count; i++ {
					if len(body) < indexBytes {
						return imageDimensions{}, false
					}
					var value uint32
					if indexBytes == 1 {
						value = uint32(body[0] & 0x7f)
					} else {
						value = uint32(binary.BigEndian.Uint16(body[:2]) & 0x7fff)
					}
					associations[item] = append(associations[item], value)
					body = body[indexBytes:]
				}
			}
		}
	}
	var result imageDimensions
	for _, index := range associations[primary] {
		prop := properties[index].dim
		if result.width == 0 && prop.width != 0 && prop.height != 0 {
			result.width, result.height = prop.width, prop.height
		}
		if result.bits == 0 && prop.bits != 0 && prop.channels != 0 {
			result.bits, result.channels = prop.bits, prop.channels
		}
	}
	if result.bits == 0 {
		var findChannels func(uint32, int) (uint32, uint32, bool)
		findChannels = func(item uint32, depth int) (uint32, uint32, bool) {
			for _, index := range associations[item] {
				prop := properties[index].dim
				if prop.bits != 0 && prop.channels != 0 {
					return prop.bits, prop.channels, true
				}
			}
			if depth >= 3 {
				return 0, 0, false
			}
			for _, tile := range tiles[item] {
				if bits, channels, found := findChannels(tile, depth+1); found {
					return bits, channels, true
				}
			}
			return 0, 0, false
		}
		result.bits, result.channels, _ = findChannels(primary, 0)
	}
	if alpha && result.channels != 0 {
		result.channels++
	}
	return result, result.width != 0 && result.height != 0 && result.bits != 0 && result.channels != 0
}
