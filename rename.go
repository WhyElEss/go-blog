package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Pictures are named after their post: the cover <slug>-cover, pictures in
// the text <slug>-01, <slug>-02 and so on (the editor sends the slug, or
// the title while the slug is empty).

// nextNumbered names the next picture under base ("halloween-candies" →
// "halloween-candies-03"): one above the highest number an image already
// has under that stem, in any month's folder.
func (d *Data) nextNumbered(base string) string {
	top := 0
	for key := range d.Images {
		stem := strings.TrimSuffix(path.Base(key), path.Ext(key))
		rest, ok := strings.CutPrefix(stem, base+"-")
		if !ok || len(rest) < 2 {
			continue
		}
		if strings.Trim(rest, "0123456789") != "" {
			continue
		}
		if n, _ := strconv.Atoi(rest); n > top {
			top = n
		}
	}
	return fmt.Sprintf("%s-%02d", base, top+1)
}

// imageRef finds /img/ URLs in Markdown and settings. The key ends with
// its extension, so a fragment, a title, a bracket or a full stop after
// it are left out.
var imageRef = regexp.MustCompile(`/img/([0-9A-Za-z_/-]+(?:\.[0-9A-Za-z_-]+)*\.[0-9A-Za-z]+)`)

// variantRef splits a smaller copy's key ("2026/11/x-w600.jpg").
var variantRef = regexp.MustCompile(`^(.+)-w([0-9]+)(\.[0-9a-z]+)$`)

// renamedKey maps a stored key, or one of its smaller copies, through
// renames. ok is false when the key is not renamed.
func renamedKey(key string, renames map[string]string) (string, bool) {
	if to, ok := renames[key]; ok {
		return to, true
	}
	if m := variantRef.FindStringSubmatch(key); m != nil {
		if to, ok := renames[m[1]+m[3]]; ok {
			ext := path.Ext(to)
			return strings.TrimSuffix(to, ext) + "-w" + m[2] + ext, true
		}
	}
	return "", false
}

func rewriteImageURLs(s string, renames map[string]string) string {
	return imageRef.ReplaceAllStringFunc(s, func(u string) string {
		if to, ok := renamedKey(strings.TrimPrefix(u, "/img/"), renames); ok {
			return "/img/" + to
		}
		return u
	})
}

// imageRename moves one stored image to a new key in the same folder.
type imageRename struct {
	From, To, Post string
}

// planImageRenames names every picture after the post it belongs to: the
// cover <slug>-cover, the pictures in the text <slug>-01, -02… in the order
// they first appear (the original text, then the translation). A picture
// in several posts belongs to the oldest one. Images used by no post (the
// logo, the icon, unused uploads) keep their names.
func planImageRenames(d *Data) ([]imageRename, error) {
	posts := slices.Clone(d.Posts)
	slices.SortStableFunc(posts, func(a, b *Post) int { return a.CreatedAt.Compare(b.CreatedAt) })
	claimed := map[string]bool{}
	var plan []imageRename
	add := func(p *Post, key, stem string) {
		claimed[key] = true
		to := path.Dir(key) + "/" + stem + path.Ext(key)
		if to != key {
			plan = append(plan, imageRename{From: key, To: to, Post: p.Slug})
		}
	}
	for _, p := range posts {
		base := imageName(p.Slug)
		if key, ok := strings.CutPrefix(p.Cover, "/img/"); ok && d.Images[key] != nil && !claimed[key] {
			add(p, key, base+"-cover")
		}
		text := p.Body
		if p.Translation != nil {
			text += "\n" + p.Translation.Body
		}
		n := 0
		for _, m := range imageRef.FindAllStringSubmatch(text, -1) {
			key := m[1]
			if d.Images[key] == nil || claimed[key] {
				continue
			}
			n++
			add(p, key, fmt.Sprintf("%s-%02d", base, n))
		}
	}
	// A new name must not be taken by an image that keeps its own.
	moving := map[string]bool{}
	for _, r := range plan {
		moving[r.From] = true
	}
	seen := map[string]bool{}
	for _, r := range plan {
		if seen[r.To] {
			return nil, fmt.Errorf("two images would be named %s", r.To)
		}
		seen[r.To] = true
		if d.Images[r.To] != nil && !moving[r.To] {
			return nil, fmt.Errorf("%s → %s: the name is taken", r.From, r.To)
		}
	}
	return plan, nil
}

// imageFiles lists an image's files relative to the images folder: the
// full copy and every smaller one.
func imageFiles(im *Image, key string) []string {
	var files []string
	for _, w := range im.Widths {
		files = append(files, strings.TrimPrefix(variantURL(key, im.Width, w), "/img/"))
	}
	return files
}

// applyImageRenames moves the files and rewrites every reference: covers,
// texts, translations, the logo and icon, the Ghost redirects. The old
// URLs redirect to the new ones (Data.Redirects under /img/), so links
// already out there keep working. The new names are hard links; cleanup
// removes the old ones and must run only once blog.json is saved, so a
// failure on the way leaves the blog working on the old names.
func applyImageRenames(d *Data, dir string, plan []imageRename) (cleanup func(), err error) {
	renames := map[string]string{}
	for _, r := range plan {
		renames[r.From] = r.To
	}
	var linked, old []string
	undo := func() {
		for _, f := range linked {
			os.Remove(filepath.Join(dir, filepath.FromSlash(f)))
		}
	}
	for _, r := range plan {
		im := d.Images[r.From]
		from, to := imageFiles(im, r.From), imageFiles(im, r.To)
		for i := range from {
			dst := filepath.Join(dir, filepath.FromSlash(to[i]))
			if _, err := os.Lstat(dst); err == nil {
				undo()
				return nil, fmt.Errorf("%s already exists", to[i])
			}
			if err := os.Link(filepath.Join(dir, filepath.FromSlash(from[i])), dst); err != nil {
				undo()
				return nil, err
			}
			linked = append(linked, to[i])
			old = append(old, from[i])
		}
	}
	images := map[string]*Image{}
	for key, im := range d.Images {
		if to, ok := renames[key]; ok {
			c := *im
			c.Key = to
			images[to] = &c
		} else {
			images[key] = im
		}
	}
	d.Images = images
	for _, p := range d.Posts {
		p.Cover = rewriteImageURLs(p.Cover, renames)
		p.Body = rewriteImageURLs(p.Body, renames)
		if p.Translation != nil {
			p.Translation.Body = rewriteImageURLs(p.Translation.Body, renames)
		}
	}
	s := &d.Settings
	s.Logo, s.LogoDark, s.Icon = rewriteImageURLs(s.Logo, renames), rewriteImageURLs(s.LogoDark, renames), rewriteImageURLs(s.Icon, renames)
	if d.Redirects == nil {
		d.Redirects = map[string]string{}
	}
	for from, key := range d.Redirects {
		if to, ok := renames[key]; ok {
			d.Redirects[from] = to
		}
	}
	for _, r := range plan {
		d.Redirects["/img/"+r.From] = r.To
	}
	return func() {
		for _, f := range old {
			os.Remove(filepath.Join(dir, filepath.FromSlash(f)))
		}
	}, nil
}

// movedImage returns the new key of a renamed image, or of one of its
// smaller copies; "" when the path was never renamed.
func (d *Data) movedImage(key string) string {
	if to, ok := d.Redirects["/img/"+key]; ok {
		return to
	}
	if m := variantRef.FindStringSubmatch(key); m != nil {
		if to, ok := d.Redirects["/img/"+m[1]+m[3]]; ok {
			ext := path.Ext(to)
			return strings.TrimSuffix(to, ext) + "-w" + m[2] + ext
		}
	}
	return ""
}

// runRenameImages renames the pictures of every post after the post, as
// new uploads are named. Run it with the blog stopped, as the import;
// -n prints the plan and changes nothing.
//
//	blog rename-images [-n]
func runRenameImages(args []string) error {
	dry := false
	switch {
	case len(args) == 1 && args[0] == "-n":
		dry = true
	case len(args) != 0:
		return errors.New("usage: blog rename-images [-n]")
	}
	dataDir := env("DATA_DIR", "./data")
	store, err := openStore(filepath.Join(dataDir, "blog.json"))
	if err != nil {
		return err
	}
	var cleanup func()
	err = store.update(func(d *Data) error {
		plan, err := planImageRenames(d)
		if err != nil {
			return err
		}
		for _, r := range plan {
			log.Printf("%s: %s → %s", r.Post, r.From, r.To)
		}
		log.Printf("%d images to rename", len(plan))
		if dry || len(plan) == 0 {
			return errNoChange
		}
		cleanup, err = applyImageRenames(d, filepath.Join(dataDir, "images"), plan)
		return err
	})
	if err != nil {
		return err
	}
	if cleanup != nil {
		cleanup()
		log.Print("renamed; the old URLs redirect to the new ones")
	}
	return nil
}
