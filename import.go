package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// importFile is what tools/ghost2md.py writes: posts already in Markdown,
// images still referenced by their Ghost paths (/content/images/…).
type importFile struct {
	Settings *Settings    `json:"settings"`
	Posts    []importPost `json:"posts"`
}

type importPost struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Excerpt     string    `json:"excerpt"`
	Cover       string    `json:"cover"`
	Tags        []string  `json:"tags"`
	Page        bool      `json:"page"`
	Lang        string    `json:"lang"`
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var ghostImage = regexp.MustCompile(`/content/images/[^\s"'()<>]+`)

// runImport adds posts from an import file to the blog in DATA_DIR, with
// every Ghost image they use run through the upload pipeline. Posts whose
// slug already exists are skipped, so it can run again after a fix.
//
//	blog import <file.json> <ghost content/images dir>
func runImport(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: blog import <file.json> <ghost images dir>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var in importFile
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	ghostDir := args[1]
	dataDir := env("DATA_DIR", "./data")
	imagesDir := filepath.Join(dataDir, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return err
	}
	store, err := openStore(filepath.Join(dataDir, "blog.json"))
	if err != nil {
		return err
	}

	// key for every Ghost path, converted once.
	keys := map[string]string{}
	store.view(func(d *Data) {
		for old, key := range d.Redirects {
			keys[old] = key
		}
	})
	convert := func(old string) string {
		if key, ok := keys[old]; ok {
			return key
		}
		rel := strings.TrimPrefix(old, "/content/images/")
		data, err := os.ReadFile(filepath.Join(ghostDir, filepath.FromSlash(rel)))
		if err != nil {
			log.Printf("skip %s: %v", old, err)
			return ""
		}
		when := time.Now()
		if t, err := time.Parse("2006/01", path.Dir(rel)); err == nil {
			when = t
		}
		exists := func(key string) bool {
			_, known := store.image(key)
			return known || fileExists(imagesDir, key)
		}
		im, err := processImage(imagesDir, path.Base(rel), data, when, exists)
		if err != nil {
			log.Printf("skip %s: %v", old, err)
			return ""
		}
		im.Uploaded = when
		if err := store.update(func(d *Data) error {
			d.Images[im.Key] = im
			if d.Redirects == nil {
				d.Redirects = map[string]string{}
			}
			d.Redirects[old] = im.Key
			return nil
		}); err != nil {
			log.Printf("record %s: %v", old, err)
			return ""
		}
		keys[old] = im.Key
		log.Printf("image %s -> /img/%s (%d×%d, %d copies)", old, im.Key, im.Width, im.Height, len(im.Widths))
		return im.Key
	}
	rewrite := func(s string) string {
		return ghostImage.ReplaceAllStringFunc(s, func(old string) string {
			if key := convert(old); key != "" {
				return "/img/" + key
			}
			return old
		})
	}

	if in.Settings != nil {
		s := *in.Settings
		s.Logo, s.LogoDark, s.Icon = rewrite(s.Logo), rewrite(s.LogoDark), rewrite(s.Icon)
		if s.PostsPerPage <= 0 {
			s.PostsPerPage = 10
		}
		if err := store.update(func(d *Data) error { d.Settings = s; return nil }); err != nil {
			return err
		}
	}
	added := 0
	for _, ip := range in.Posts {
		exists := false
		store.view(func(d *Data) { exists = d.postBySlug(ip.Slug) != nil })
		if exists {
			log.Printf("post %s exists, skipped", ip.Slug)
			continue
		}
		p := &Post{
			ID: newID(), Slug: ip.Slug, Title: ip.Title, Body: rewrite(ip.Body), Excerpt: ip.Excerpt,
			Cover: rewrite(ip.Cover), Tags: ip.Tags, Page: ip.Page, Lang: normLang(ip.Lang),
			PublishedAt: ip.PublishedAt, CreatedAt: ip.CreatedAt, UpdatedAt: ip.UpdatedAt,
		}
		if err := store.update(func(d *Data) error { d.Posts = append(d.Posts, p); return nil }); err != nil {
			return err
		}
		added++
		log.Printf("post %s added", p.Slug)
	}
	log.Printf("import done: %d posts added, %d images", added, len(keys))
	return nil
}
