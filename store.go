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
	// Lang is the post's language (en, ru): the page's lang attribute and
	// the date format.
	Lang string `json:"lang,omitempty"`
	// PublishedAt in the future keeps a non-draft post hidden until then.
	PublishedAt time.Time `json:"published_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	Uploaded time.Time `json:"uploaded"`
}

// Link is a navigation entry in the header or footer.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Settings struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Lang is the language of the site's own texts (en, ru).
	Lang         string `json:"lang"`
	Nav          []Link `json:"nav,omitempty"`
	FooterNav    []Link `json:"footer_nav,omitempty"`
	Logo         string `json:"logo,omitempty"`      // image URL; empty shows the title
	LogoDark     string `json:"logo_dark,omitempty"` // logo for dark mode; empty uses Logo
	Icon         string `json:"icon,omitempty"`      // square image URL for the favicon
	PostsPerPage int    `json:"posts_per_page"`
}

type Data struct {
	Posts    []*Post           `json:"posts"`
	Images   map[string]*Image `json:"images"`
	Settings Settings          `json:"settings"`
	// Redirects maps Ghost's image paths (/content/images/2025/11/x.jpg)
	// to the imported image keys.
	Redirects map[string]string `json:"redirects,omitempty"`
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

// sortPosts keeps the newest publication date first.
func (d *Data) sortPosts() {
	slices.SortStableFunc(d.Posts, func(a, b *Post) int { return b.PublishedAt.Compare(a.PublishedAt) })
}

// imageUses counts how many posts and settings refer to the image.
func (d *Data) imageUses(key string) int {
	url := "/img/" + key
	n := 0
	for _, p := range d.Posts {
		if strings.Contains(p.Body, url) || p.Cover == url {
			n++
		}
	}
	if d.Settings.Logo == url || d.Settings.LogoDark == url || d.Settings.Icon == url {
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
