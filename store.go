package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	errNoChange = errors.New("no change")
	errNotFound = errors.New("not found")
)

// Post is a blog post or, with Page set, a standalone page (About, Contact)
// that is left out of the post list and the feed.
type Post struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// Body is Markdown.
	Body string `json:"body"`
	// Excerpt is shown on the list and in link previews; empty means the
	// start of the text.
	Excerpt string   `json:"excerpt,omitempty"`
	Cover   string   `json:"cover,omitempty"` // image URL, usually /img/...
	Tags    []string `json:"tags,omitempty"`
	Page    bool     `json:"page,omitempty"`
	Draft   bool     `json:"draft,omitempty"`
	// Lang is the language of Title, Body and Excerpt (en, ru). Readers who
	// want the other language get Translation when there is one, else this.
	Lang        string       `json:"lang,omitempty"`
	Translation *Translation `json:"translation,omitempty"`
	// SendBluesky asks for a link on Bluesky once the post is live;
	// Bluesky records it sent, BlueskyError the last failed try.
	SendBluesky  bool         `json:"send_bluesky,omitempty"`
	Bluesky      *Syndication `json:"bluesky,omitempty"`
	BlueskyError string       `json:"bluesky_error,omitempty"`
	// PublishedAt in the future keeps a non-draft post hidden until then.
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Translation is a post's text in the blog's other language, the one its
// Lang is not. Cover, tags, date and URL are shared.
type Translation struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Excerpt string `json:"excerpt,omitempty"`
}

// postText is a post in one language.
type postText struct {
	Lang, Title, Body, Excerpt string
}

// text returns the post in lang, or in its own language when it has no
// translation into lang.
func (p *Post) text(lang string) postText {
	if p.Translation != nil && normLang(lang) != normLang(p.Lang) {
		tr := p.Translation
		return postText{Lang: otherLang(p.Lang), Title: tr.Title, Body: tr.Body, Excerpt: tr.Excerpt}
	}
	return postText{Lang: normLang(p.Lang), Title: p.Title, Body: p.Body, Excerpt: p.Excerpt}
}

// live reports whether visitors can see the post.
func (p *Post) live(now time.Time) bool {
	return !p.Draft && !p.PublishedAt.After(now)
}

func (p *Post) hasTag(slug string) bool {
	for _, t := range p.Tags {
		if tagSlug(t) == slug {
			return true
		}
	}
	return false
}

// Image is an uploaded picture. Key is its path under /img/ for the largest
// version ("2026/10/turntable.jpg"); smaller copies sit next to it as
// "turntable-w600.jpg" for every width in Widths below the full one.
type Image struct {
	Key      string    `json:"key"`
	Name     string    `json:"name,omitempty"` // original file name
	Width    int       `json:"width"`
	Height   int       `json:"height"`
	Widths   []int     `json:"widths"` // ascending; the last is Width
	Bytes    int64     `json:"bytes"`
	Quality  int       `json:"quality,omitempty"` // JPEG quality of the copies; 0 before 2026-11-06
	Uploaded time.Time `json:"uploaded"`
}

// Link is a navigation entry in the header or footer. Label is in the
// site's default language, LabelAlt (optional) in the other one.
type Link struct {
	Label    string `json:"label"`
	LabelAlt string `json:"label_alt,omitempty"`
	URL      string `json:"url"`
}

type Settings struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// DescriptionAlt is the description in the language Lang is not.
	DescriptionAlt string `json:"description_alt,omitempty"`
	// Lang is the default language: for readers whose browser asks for
	// neither Russian nor English, and for the feed at /rss/.
	Lang      string `json:"lang"`
	Nav       []Link `json:"nav,omitempty"`
	FooterNav []Link `json:"footer_nav,omitempty"`
	Logo      string `json:"logo,omitempty"`      // image URL; empty shows the title
	LogoDark  string `json:"logo_dark,omitempty"` // logo for dark mode; empty uses Logo
	Icon      string `json:"icon,omitempty"`      // square image URL for the favicon
	// Profiles linked under every post for comments; empty ones are left out.
	Bluesky      string `json:"bluesky,omitempty"`
	Mastodon     string `json:"mastodon,omitempty"`
	X            string `json:"x,omitempty"`
	PostsPerPage int    `json:"posts_per_page"`
}

type Data struct {
	Posts    []*Post           `json:"posts"`
	Images   map[string]*Image `json:"images"`
	Settings Settings          `json:"settings"`
	// Redirects maps Ghost's image paths (/content/images/2025/11/x.jpg)
	// to the imported image keys.
	Redirects map[string]string `json:"redirects,omitempty"`
	// SlugRedirects maps the slug of a post merged into another one to
	// where it went ("/other/?lang=ru").
	SlugRedirects map[string]string `json:"slug_redirects,omitempty"`
}

func defaultSettings() Settings {
	return Settings{Title: "Blog", Lang: "en", PostsPerPage: 10}
}

func (d *Data) post(id string) *Post {
	for _, p := range d.Posts {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (d *Data) postBySlug(slug string) *Post {
	for _, p := range d.Posts {
		if p.Slug == slug {
			return p
		}
	}
	return nil
}

// neighbours returns the live posts published just before and just after p,
// skipping pages. A page or a post visitors cannot see yet has none. The
// returned posts are copies, safe to use after the lock is released.
func (d *Data) neighbours(p *Post, now time.Time) (older, newer *Post) {
	if p.Page || !p.live(now) {
		return nil, nil
	}
	var prev *Post // the newer live post seen last, walking newest first
	for i, q := range d.Posts {
		if q.Page || !q.live(now) {
			continue
		}
		if q != p {
			prev = q
			continue
		}
		if prev != nil {
			c := *prev
			newer = &c
		}
		for _, r := range d.Posts[i+1:] {
			if !r.Page && r.live(now) {
				c := *r
				older = &c
				break
			}
		}
		break
	}
	return older, newer
}

// sortPosts keeps the newest publication date first.
func (d *Data) sortPosts() {
	slices.SortStableFunc(d.Posts, func(a, b *Post) int { return b.PublishedAt.Compare(a.PublishedAt) })
}

// imageUses counts how many posts and settings refer to the image.
func (d *Data) imageUses(key string) int {
	url := "/img/" + key
	n := 0
	for _, p := range d.Posts {
		if strings.Contains(p.Body, url) || p.Cover == url || (p.Translation != nil && strings.Contains(p.Translation.Body, url)) {
			n++
		}
	}
	if d.Settings.Logo == url || d.Settings.LogoDark == url || d.Settings.Icon == url {
		n++
	}
	return n
}

// tagRow is a tag as the settings page lists it. Posts share a tag by its
// slug, so "Vinyl" and "vinyl" are one tag, named by its first spelling.
type tagRow struct {
	Name, Slug string
	Count      int
}

// tagRows lists every tag on posts and pages, drafts included, most used
// first.
func (d *Data) tagRows() []tagRow {
	var rows []tagRow
	at := map[string]int{}
	for _, p := range d.Posts {
		for _, tg := range p.Tags {
			s := tagSlug(tg)
			i, ok := at[s]
			if !ok {
				i = len(rows)
				at[s] = i
				rows = append(rows, tagRow{Name: tg, Slug: s})
			}
			rows[i].Count++
		}
	}
	slices.SortStableFunc(rows, func(a, b tagRow) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return rows
}

// retag puts name in place of the tag with this slug on every post, or
// drops the tag when name is empty. A name another tag already has merges
// the two. It reports how many posts changed.
func (d *Data) retag(slug, name string) int {
	n := 0
	for _, p := range d.Posts {
		if !p.hasTag(slug) {
			continue
		}
		var tags []string
		seen := map[string]bool{}
		for _, tg := range p.Tags {
			if tagSlug(tg) == slug {
				tg = name
			}
			if tg == "" || seen[tagSlug(tg)] {
				continue
			}
			seen[tagSlug(tg)] = true
			tags = append(tags, tg)
		}
		p.Tags = tags
		n++
	}
	return n
}

type Store struct {
	mu   sync.RWMutex
	path string
	data Data
}

func openStore(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s.data = Data{Settings: defaultSettings(), Images: map[string]*Image{}}
		return s, s.saveLocked()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.data.Images == nil {
		s.data.Images = map[string]*Image{}
	}
	if s.data.Settings.PostsPerPage <= 0 {
		s.data.Settings.PostsPerPage = 10
	}
	if s.data.Settings.Lang == "" {
		s.data.Settings.Lang = "en"
	}
	s.data.sortPosts()
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) view(fn func(d *Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.data)
}

// update runs fn under the write lock and saves the file. fn must check
// everything before mutating: an error leaves the data as fn left it,
// unsaved. errNoChange skips the save without an error.
func (s *Store) update(fn func(d *Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.data); err != nil {
		if errors.Is(err, errNoChange) {
			return nil
		}
		return err
	}
	s.data.sortPosts()
	return s.saveLocked()
}

// image returns a copy of the image record, for the Markdown renderer.
func (s *Store) image(key string) (Image, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	im, ok := s.data.Images[key]
	if !ok {
		return Image{}, false
	}
	return *im, true
}

func randomString(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func newID() string { return randomString(10) }
