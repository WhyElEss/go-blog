package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// needsRecompress says whether a stored image differs from what an upload
// would make now: a larger full copy, other copy widths, or another JPEG
// quality. GIF and SVG are stored as uploaded and never recompressed.
func needsRecompress(im *Image) bool {
	ext := path.Ext(im.Key)
	if ext != ".jpg" && ext != ".png" {
		return false
	}
	if fw, fh := fitSize(im.Width, im.Height); fw != im.Width || fh != im.Height {
		return true
	}
	if !slices.Equal(im.Widths, copyWidths(im.Width)) {
		return true
	}
	return ext == ".jpg" && im.Quality != jpegQuality
}

// recompressed is one image's new copies, written next to the old files
// under the suffix recompressSuffix until blog.json is saved.
type recompressed struct {
	key           string
	width, height int
	widths        []int
	bytes         int64
}

// The suffix ends in .tmp, so the standby sync and /img/ both ignore the
// files while they wait.
const recompressSuffix = ".recompress.tmp"

// recompressImage makes the new copies of one stored image from its full
// copy, the largest one the blog keeps (the upload itself is not stored).
// With write it puts them next to the old files under recompressSuffix.
// It returns the new record and the bytes of the old and new files.
func recompressImage(dir string, im *Image, write bool) (r recompressed, before, after int64, err error) {
	for _, f := range imageFiles(im, im.Key) {
		if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
			before += st.Size()
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(im.Key)))
	if err != nil {
		return r, 0, 0, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return r, 0, 0, fmt.Errorf("%s: %w", im.Key, err)
	}
	b := src.Bounds()
	fw, fh := fitSize(b.Dx(), b.Dy())
	var full image.Image = toRGBA(src)
	if fw != b.Dx() || fh != b.Dy() {
		full = scale(full, fw, fh)
	}
	copies, err := encodeCopies(full, path.Ext(im.Key))
	if err != nil {
		return r, 0, 0, fmt.Errorf("%s: %w", im.Key, err)
	}
	r = recompressed{key: im.Key, width: fw, height: fh}
	for _, c := range copies {
		r.widths = append(r.widths, c.Width)
		after += int64(len(c.Data))
		if !write {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(variantURL(im.Key, fw, c.Width), "/img/")))
		if err := os.WriteFile(p+recompressSuffix, c.Data, 0o644); err != nil {
			return r, 0, 0, err
		}
	}
	r.bytes = int64(len(copies[len(copies)-1].Data))
	return r, before, after, nil
}

// runRecompressImages brings every stored JPEG and PNG to the current
// upload settings (maxImageSide, variantWidths, jpegQuality). The new
// copies are made from the stored full copy, so a JPEG is encoded a second
// time. Names stay; a smaller copy that is no longer made (-w1400)
// redirects to the next larger one, or the full copy (Data.movedImage). Run it with the blog
// stopped; -n prints the plan and the sizes and writes nothing.
//
//	blog recompress-images [-n]
func runRecompressImages(args []string) error {
	dry := false
	switch {
	case len(args) == 1 && args[0] == "-n":
		dry = true
	case len(args) != 0:
		return errors.New("usage: blog recompress-images [-n]")
	}
	dataDir := env("DATA_DIR", "./data")
	dir := filepath.Join(dataDir, "images")
	store, err := openStore(filepath.Join(dataDir, "blog.json"))
	if err != nil {
		return err
	}
	var todo []Image
	store.view(func(d *Data) {
		for _, im := range d.Images {
			if needsRecompress(im) {
				todo = append(todo, *im)
			}
		}
	})
	slices.SortFunc(todo, func(a, b Image) int { return strings.Compare(a.Key, b.Key) })

	var done []recompressed
	var temps []string
	removeTemps := func() {
		for _, p := range temps {
			os.Remove(p)
		}
	}
	var before, after int64
	for _, im := range todo {
		r, b, a, err := recompressImage(dir, &im, !dry)
		for _, w := range r.widths {
			temps = append(temps, filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(variantURL(im.Key, r.width, w), "/img/")))+recompressSuffix)
		}
		if err != nil {
			removeTemps()
			return err
		}
		log.Printf("%s: %d×%d %v → %d×%d %v, %d KB → %d KB", im.Key, im.Width, im.Height, im.Widths, r.width, r.height, r.widths, b/1024, a/1024)
		before, after = before+b, after+a
		done = append(done, r)
	}
	log.Printf("%d images: %.1f MB → %.1f MB", len(done), float64(before)/1e6, float64(after)/1e6)
	if dry || len(done) == 0 {
		return nil
	}

	old := map[string][]string{}
	err = store.update(func(d *Data) error {
		for _, r := range done {
			im := d.Images[r.key]
			if im == nil {
				return fmt.Errorf("%s disappeared", r.key)
			}
			old[r.key] = imageFiles(im, r.key)
			im.Width, im.Height, im.Widths, im.Bytes = r.width, r.height, r.widths, r.bytes
			if path.Ext(r.key) == ".jpg" {
				im.Quality = jpegQuality
			}
		}
		return nil
	})
	if err != nil {
		removeTemps()
		return err
	}
	// blog.json names the new sizes now: put the new files in place, then
	// drop the copies that are no longer made.
	for _, r := range done {
		keep := map[string]bool{}
		for _, w := range r.widths {
			f := strings.TrimPrefix(variantURL(r.key, r.width, w), "/img/")
			keep[f] = true
			p := filepath.Join(dir, filepath.FromSlash(f))
			if err := os.Rename(p+recompressSuffix, p); err != nil {
				return err
			}
		}
		for _, f := range old[r.key] {
			if !keep[f] {
				os.Remove(filepath.Join(dir, filepath.FromSlash(f)))
			}
		}
	}
	log.Print("recompressed")
	return nil
}
