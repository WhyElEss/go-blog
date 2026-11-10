package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder only: uploads may be WebP
)

// Uploads are re-encoded, which also drops their metadata (camera, GPS).
// The largest copy is at most maxImageSide on its longer side, so a phone
// screenshot is as heavy as a landscape photo: 1440 px is the 720 px text
// column on a 2x screen. Smaller copies for the srcset are made at the
// widths below its width. `blog recompress-images` brings older uploads
// to these settings (recompress.go).
const (
	maxImageSide   = 1440
	maxImagePixels = 60_000_000 // decoding needs 4 bytes per pixel
	jpegQuality    = 80
)

var variantWidths = []int{600, 1000}

var errImageFormat = errors.New("unsupported image format: use JPEG, PNG, GIF, WebP or SVG")

// variantURL is the /img/ URL of the copy of key at width w.
func variantURL(key string, full, w int) string {
	if w >= full {
		return "/img/" + key
	}
	ext := path.Ext(key)
	return fmt.Sprintf("/img/%s-w%d%s", strings.TrimSuffix(key, ext), w, ext)
}

// imageVersion is the query that tells one upload under a name from the
// next: /img/ answers are cached for a year as immutable (handleImage), so
// a picture deleted and uploaded again under the same name ("<slug>-cover")
// must have a new address for browsers and Cloudflare to fetch it. Names
// stay as they are; texts and fields keep the plain /img/ URL and pages
// get it with ?v=<upload time>.
func imageVersion(im Image) string {
	if im.Uploaded.IsZero() {
		return ""
	}
	return "?v=" + strconv.FormatInt(im.Uploaded.UnixMilli(), 36)
}

// imageURL is the address pages use for the copy of im at width w.
func imageURL(im Image, w int) string {
	return variantURL(im.Key, im.Width, w) + imageVersion(im)
}

// imgSrc gives an /img/ URL of a known upload its version; anything else
// is returned as it is. Call it outside the store lock.
func (a *App) imgSrc(u string) string {
	if key, ok := strings.CutPrefix(u, "/img/"); ok {
		if im, ok := a.store.image(key); ok {
			return u + imageVersion(im)
		}
	}
	return u
}

// imageName makes a file name stem from an upload's name, without image
// extensions ("photo.jpg.webp" → "photo").
func imageName(name string) string {
	stem := filepath.Base(name)
	for {
		ext := strings.ToLower(filepath.Ext(stem))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".heic":
			stem = stem[:len(stem)-len(ext)]
			continue
		}
		break
	}
	if s := slugify(stem); s != "" {
		return s
	}
	return "image"
}

// processImage resizes an upload and writes its copies under dir/YYYY/MM/.
// taken (the import's or the upload's date) picks the folder; exists says
// whether a key is already taken, so names never collide.
func processImage(dir, name string, data []byte, taken time.Time, exists func(key string) bool) (*Image, error) {
	folder := taken.Format("2006/01")
	stem := imageName(name)

	// SVG drawings are vector: stored as they are, any size.
	if w, h, ok := svgSize(data); ok {
		key := freeKey(folder, stem, ".svg", exists)
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(key)), data); err != nil {
			return nil, err
		}
		return &Image{Key: key, Name: name, Width: w, Height: h, Widths: []int{w}, Bytes: int64(len(data))}, nil
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errImageFormat
	}
	if cfg.Width*cfg.Height > maxImagePixels {
		return nil, fmt.Errorf("image too large: %d×%d pixels", cfg.Width, cfg.Height)
	}

	// Animated or not, GIFs are kept as they are: re-encoding would lose
	// frames or colours.
	if format == "gif" {
		if _, err := gif.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, errImageFormat
		}
		key := freeKey(folder, stem, ".gif", exists)
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(key)), data); err != nil {
			return nil, err
		}
		return &Image{Key: key, Name: name, Width: cfg.Width, Height: cfg.Height, Widths: []int{cfg.Width}, Bytes: int64(len(data))}, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", format, err)
	}
	orient := 1
	if format == "jpeg" {
		orient = jpegOrientation(data)
	}

	// Size after rotation, then the full copy's size.
	ow, oh := cfg.Width, cfg.Height
	if orient >= 5 {
		ow, oh = oh, ow
	}
	fw, fh := fitSize(ow, oh)
	sw, sh := fw, fh
	if orient >= 5 {
		sw, sh = fh, fw
	}
	var full image.Image = src
	if sw != cfg.Width || sh != cfg.Height {
		full = scale(src, sw, sh)
	}
	full = orientImage(toRGBA(full), orient)

	// PNG keeps transparency; everything opaque becomes JPEG.
	ext := ".jpg"
	if format == "png" && !isOpaque(full) {
		ext = ".png"
	}
	key := freeKey(folder, stem, ext, exists)
	im := &Image{Key: key, Name: name, Width: fw, Height: fh}

	copies, err := encodeCopies(full, ext)
	if err != nil {
		return nil, err
	}
	var written []string
	for _, c := range copies {
		p := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(variantURL(key, fw, c.Width), "/img/")))
		if err := writeFile(p, c.Data); err != nil {
			for _, p := range written {
				os.Remove(p)
			}
			return nil, err
		}
		written = append(written, p)
		im.Widths = append(im.Widths, c.Width)
	}
	im.Bytes = int64(len(copies[len(copies)-1].Data))
	if ext == ".jpg" {
		im.Quality = jpegQuality
	}
	return im, nil
}

// fitSize is the full copy's size for a picture of w×h: at most
// maxImageSide on the longer side.
func fitSize(w, h int) (int, int) {
	switch {
	case w >= h && w > maxImageSide:
		return maxImageSide, max(1, (h*maxImageSide+w/2)/w)
	case h > w && h > maxImageSide:
		return max(1, (w*maxImageSide+h/2)/h), maxImageSide
	}
	return w, h
}

// imageCopy is one encoded copy of an image, Width pixels wide.
type imageCopy struct {
	Width int
	Data  []byte
}

// copyWidths are the widths of the copies of a full copy fw wide,
// ascending; the last is fw.
func copyWidths(fw int) []int {
	var widths []int
	for _, w := range variantWidths {
		if w < fw {
			widths = append(widths, w)
		}
	}
	return append(widths, fw)
}

// encodeCopies encodes the full copy and its smaller copies: JPEG at
// jpegQuality, or PNG for ext ".png".
func encodeCopies(full image.Image, ext string) ([]imageCopy, error) {
	fw, fh := full.Bounds().Dx(), full.Bounds().Dy()
	var copies []imageCopy
	for _, w := range copyWidths(fw) {
		img := full
		if w != fw {
			img = scale(full, w, max(1, (fh*w+fw/2)/fw))
		}
		var buf bytes.Buffer
		var err error
		if ext == ".png" {
			err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
		} else {
			err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
		}
		if err != nil {
			return nil, err
		}
		copies = append(copies, imageCopy{w, buf.Bytes()})
	}
	return copies, nil
}

// freeKey returns folder/stem+ext, or with -2, -3… when that is taken.
func freeKey(folder, stem, ext string, exists func(string) bool) string {
	key := folder + "/" + stem + ext
	for i := 2; exists(key); i++ {
		key = fmt.Sprintf("%s/%s-%d%s", folder, stem, i, ext)
	}
	return key
}

func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// removeImageFiles deletes every copy of an image.
func removeImageFiles(dir string, im *Image) {
	for _, w := range im.Widths {
		os.Remove(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(variantURL(im.Key, im.Width, w), "/img/"))))
	}
}

func scale(src image.Image, w, h int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Bounds().Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// orientImage applies an EXIF orientation (1–8) to the pixels.
func orientImage(src *image.RGBA, orient int) *image.RGBA {
	if orient < 2 || orient > 8 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if orient >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orient {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // upside down
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored upside down
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // turned 90° clockwise to view
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // turned 90° counter-clockwise to view
				dx, dy = y, w-1-x
			}
			si := y*src.Stride + x*4
			di := dy*dst.Stride + dx*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// jpegOrientation reads the EXIF orientation tag of a JPEG; 1 (as stored)
// when there is none.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 { // image data starts: no EXIF before it
			return 1
		}
		size := int(b[i+2])<<8 | int(b[i+3])
		if size < 2 || i+2+size > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(p []byte) int { return int(p[0]) | int(p[1])<<8 }
		u32 = func(p []byte) int { return int(p[0]) | int(p[1])<<8 | int(p[2])<<16 | int(p[3])<<24 }
	case "MM":
		u16 = func(p []byte) int { return int(p[1]) | int(p[0])<<8 }
		u32 = func(p []byte) int { return int(p[3]) | int(p[2])<<8 | int(p[1])<<16 | int(p[0])<<24 }
	default:
		return 1
	}
	off := u32(t[4:8])
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := u16(t[off : off+2])
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if u16(t[e:e+2]) == 0x0112 {
			v := u16(t[e+8 : e+10])
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

var (
	svgRoot    = regexp.MustCompile(`(?s)<svg\b[^>]*>`)
	svgAttr    = regexp.MustCompile(`\s(width|height|viewBox)\s*=\s*["']([^"']*)["']`)
	svgNumber  = regexp.MustCompile(`^\s*([0-9.]+)\s*(px)?\s*$`)
	svgViewBox = regexp.MustCompile(`^\s*[-0-9.]+[\s,]+[-0-9.]+[\s,]+([0-9.]+)[\s,]+([0-9.]+)\s*$`)
)

// svgSize recognises an SVG file and reads its size from width/height or,
// for relative sizes, the viewBox. Unknown sizes fall back to 1200×800.
func svgSize(data []byte) (w, h int, ok bool) {
	head := data[:min(len(data), 4096)]
	trimmed := bytes.TrimSpace(head)
	if !bytes.HasPrefix(trimmed, []byte("<?xml")) && !bytes.HasPrefix(trimmed, []byte("<svg")) && !bytes.HasPrefix(trimmed, []byte("<!--")) && !bytes.HasPrefix(trimmed, []byte("<!DOCTYPE svg")) {
		return 0, 0, false
	}
	root := svgRoot.Find(head)
	if root == nil {
		return 0, 0, false
	}
	var fw, fh, vw, vh float64
	for _, m := range svgAttr.FindAllSubmatch(root, -1) {
		v := string(m[2])
		switch string(m[1]) {
		case "width":
			if n := svgNumber.FindStringSubmatch(v); n != nil {
				fw, _ = strconv.ParseFloat(n[1], 64)
			}
		case "height":
			if n := svgNumber.FindStringSubmatch(v); n != nil {
				fh, _ = strconv.ParseFloat(n[1], 64)
			}
		case "viewBox":
			if n := svgViewBox.FindStringSubmatch(v); n != nil {
				vw, _ = strconv.ParseFloat(n[1], 64)
				vh, _ = strconv.ParseFloat(n[2], 64)
			}
		}
	}
	switch {
	case fw >= 1 && fh >= 1:
		return int(fw + .5), int(fh + .5), true
	case vw >= 1 && vh >= 1:
		return int(vw + .5), int(vh + .5), true
	}
	return 1200, 800, true
}
