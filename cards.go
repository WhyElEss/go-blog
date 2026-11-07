package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// A cover is shown at one shape everywhere: on the post page, in the post
// list and in link cards. That shape is the link cards': Bluesky's app
// shows a card picture at CARD_ASPECT_RATIO = 1200 / 630 (social-app,
// src/lib/constants.ts) and Facebook recommends 1200×630 for og:image.
// The uploaded cover is kept as it came, so the frame can be moved later;
// what is shown is a copy cut to that shape, "<cover>-card<pos>.jpg", with
// its own 600 and 1000 px copies for srcset. Post.CoverPos says where the
// frame sits along the side that is cut (0 top or left, 100 bottom or
// right); the editor moves it. The position is in the name, so a moved
// frame is a new URL and the year-long /img/ cache never serves the old
// cut. Copies are made when a post is saved and by `blog make-cards`,
// never on a request, so a standby only ever receives them. A cover with
// no cut copy (GIF, SVG, another site's, a copy not made yet) is cut to
// the same shape by CSS around its centre.
const cardW, cardH = 1200, 630

// coverPos is the post's frame position, 50 (centre) when not set.
func coverPos(p *Post) int {
	if p.CoverPos == nil {
		return 50
	}
	return min(100, max(0, *p.CoverPos))
}

// cardCrop is the 1200:630 rectangle of a w×h picture at pos.
func cardCrop(w, h, pos int) image.Rectangle {
	if w*cardH > h*cardW { // wider than a card: cut the sides
		cw := min(w, (h*cardW+cardH/2)/cardH)
		x := (w - cw) * pos / 100
		return image.Rect(x, 0, x+cw, h)
	}
	ch := min(h, (w*cardH+cardW/2)/cardW)
	y := (h - ch) * pos / 100
	return image.Rect(0, y, w, y+ch)
}

// cardSize is the cut copy's size for a w×h cover: the cut itself,
// scaled down to maxImageSide wide when wider (an upload never is).
func cardSize(w, h int) (int, int) {
	r := cardCrop(w, h, 50)
	if r.Dx() > maxImageSide {
		return maxImageSide, (maxImageSide*cardH + cardW/2) / cardW
	}
	return r.Dx(), r.Dy()
}

// cardKey is the card copy's key for a cover URL at pos ("" when the
// cover is not an uploaded JPEG or PNG).
func cardKey(cover string, pos int) string {
	key, ok := strings.CutPrefix(cover, "/img/")
	ext := path.Ext(key)
	if !ok || (ext != ".jpg" && ext != ".png") {
		return ""
	}
	return fmt.Sprintf("%s-card%d%s", strings.TrimSuffix(key, ext), pos, ext)
}

// cardRef matches the files of a cut copy (the copy and its -wNNN ones).
var cardRef = regexp.MustCompile(`-card[0-9]+(-w[0-9]+)?\.(jpg|png)$`)

// cardImage describes the cut copy of an uploaded cover at pos as an
// image of its own, for srcset and the gallery. ok is false when the
// cover cannot have one.
func cardImage(im Image, pos int) (c Image, ok bool) {
	ck := cardKey("/img/"+im.Key, pos)
	if ck == "" {
		return Image{}, false
	}
	w, h := cardSize(im.Width, im.Height)
	return Image{Key: ck, Width: w, Height: h, Widths: copyWidths(w)}, true
}

// makeCard writes the cut copy of an uploaded cover at pos and its smaller
// copies, unless they are all there.
func makeCard(dir string, im Image, pos int) error {
	c, ok := cardImage(im, pos)
	if !ok {
		return nil
	}
	have := true
	for _, f := range imageFiles(&c, c.Key) {
		if !fileExists(dir, f) {
			have = false
		}
	}
	if have {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(im.Key)))
	if err != nil {
		return err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s: %w", im.Key, err)
	}
	full := toRGBA(src)
	b := full.Bounds()
	cut := full.SubImage(cardCrop(b.Dx(), b.Dy(), pos))
	var img image.Image = toRGBA(cut)
	if c.Width != cut.Bounds().Dx() {
		img = scale(cut, c.Width, c.Height)
	}
	copies, err := encodeCopies(img, path.Ext(c.Key))
	if err != nil {
		return err
	}
	for _, cp := range copies {
		f := strings.TrimPrefix(variantURL(c.Key, c.Width, cp.Width), "/img/")
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(f)), cp.Data); err != nil {
			return err
		}
	}
	return nil
}

// postCover is the cover the post shows: its cut copy when the file is
// there, else the uploaded cover itself. ok is false when the cover is
// not an upload.
func (a *App) postCover(p *Post) (im Image, cut, ok bool) {
	src, ok := a.store.image(strings.TrimPrefix(p.Cover, "/img/"))
	if !ok || !strings.HasPrefix(p.Cover, "/img/") {
		return Image{}, false, false
	}
	if c, ok := cardImage(src, coverPos(p)); ok && fileExists(a.imagesDir, c.Key) {
		return c, true, true
	}
	return src, false, true
}

// coverCard makes the card copy of a post's cover after a save. A failure
// is logged: the post then shows its cover in link cards.
func (a *App) coverCard(id string) {
	var im Image
	var pos int
	ok := false
	a.store.view(func(d *Data) {
		if p := d.post(id); p != nil {
			if i := d.Images[strings.TrimPrefix(p.Cover, "/img/")]; i != nil && strings.HasPrefix(p.Cover, "/img/") {
				im, pos, ok = *i, coverPos(p), true
			}
		}
	})
	if ok {
		if err := makeCard(a.imagesDir, im, pos); err != nil {
			log.Printf("card copy: %v", err)
		}
	}
	a.pruneCards()
}

// pruneCards removes card copies no post's cover asks for any more (the
// cover or its frame changed, the post was deleted). An uploaded image
// whose own name ends in -cardN is an image, not a copy, and stays.
func (a *App) pruneCards() {
	var wanted, images map[string]bool
	a.store.view(func(d *Data) {
		wanted, images = map[string]bool{}, map[string]bool{}
		for _, p := range d.Posts {
			im := d.Images[strings.TrimPrefix(p.Cover, "/img/")]
			if im == nil || !strings.HasPrefix(p.Cover, "/img/") {
				continue
			}
			if c, ok := cardImage(*im, coverPos(p)); ok {
				for _, f := range imageFiles(&c, c.Key) {
					wanted[f] = true
				}
			}
		}
		for key, im := range d.Images {
			for _, f := range imageFiles(im, key) {
				images[f] = true
			}
		}
	})
	for _, f := range unwantedCards(a.imagesDir, wanted, images) {
		os.Remove(filepath.Join(a.imagesDir, filepath.FromSlash(f)))
	}
}

func unwantedCards(dir string, wanted, images map[string]bool) []string {
	var out []string
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		f := filepath.ToSlash(rel)
		if cardRef.MatchString(f) && !wanted[f] && !images[f] {
			out = append(out, f)
		}
		return nil
	})
	return out
}

// runMakeCards makes the card copies of every post's cover and removes
// those no post asks for. It writes only image files, never blog.json,
// so it may run while the blog runs.
//
//	blog make-cards
func runMakeCards(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: blog make-cards")
	}
	dataDir := env("DATA_DIR", "./data")
	store, err := openStore(filepath.Join(dataDir, "blog.json"))
	if err != nil {
		return err
	}
	a := &App{store: store, imagesDir: filepath.Join(dataDir, "images")}
	type job struct {
		slug string
		im   Image
		pos  int
	}
	var jobs []job
	store.view(func(d *Data) {
		for _, p := range d.Posts {
			if cardKey(p.Cover, 50) == "" {
				continue
			}
			if im := d.Images[strings.TrimPrefix(p.Cover, "/img/")]; im != nil {
				jobs = append(jobs, job{p.Slug, *im, coverPos(p)})
			}
		}
	})
	for _, j := range jobs {
		if err := makeCard(a.imagesDir, j.im, j.pos); err != nil {
			return fmt.Errorf("%s: %w", j.slug, err)
		}
		c, _ := cardImage(j.im, j.pos)
		log.Printf("%s: %s %d×%d %v", j.slug, c.Key, c.Width, c.Height, c.Widths)
	}
	a.pruneCards()
	log.Printf("%d card copies", len(jobs))
	return nil
}
