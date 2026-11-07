package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// blueskyText goes above the link card. Crossposter, which copies the
// account's posts to X and Mastodon, appends the card's URL to it there.
const blueskyText = "New blog post"

// blueskyThumbMax is the largest preview image the external embed takes
// (app.bsky.embed.external, thumb maxSize).
const blueskyThumbMax = 1_000_000

// Syndication records a post sent to another site.
type Syndication struct {
	URL    string    `json:"url,omitempty"` // the post there, for people
	URI    string    `json:"uri,omitempty"` // its at:// record
	SentAt time.Time `json:"sent_at"`
}

// bluesky posts links to new blog posts with an app password.
type bluesky struct {
	service, handle, password string
	client                    *http.Client
}

func newBluesky(cfg Config) *bluesky {
	if cfg.BlueskyHandle == "" || cfg.BlueskyPassword == "" {
		return nil
	}
	return &bluesky{
		service: strings.TrimRight(cfg.BlueskyService, "/"), handle: cfg.BlueskyHandle, password: cfg.BlueskyPassword,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// blueskyCard is what the link card shows.
type blueskyCard struct {
	URL, Title, Description, Lang string
	Thumb                         []byte // JPEG/PNG/GIF/WebP, or nil
	ThumbType                     string
}

// xrpcError is an error answer from the server: {"error": "...", "message": "..."}.
type xrpcError struct {
	Status        int
	Code, Message string
}

func (e *xrpcError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Code, e.Status)
}

// call sends one XRPC request; body is JSON unless contentType is set.
func (b *bluesky) call(ctx context.Context, method, token string, body any, contentType string, out any) error {
	var rd io.Reader
	if raw, ok := body.([]byte); ok {
		rd = bytes.NewReader(raw)
	} else if body != nil {
		j, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd, contentType = bytes.NewReader(j), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.service+"/xrpc/"+method, rd)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error, Message string }
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("%s: %w", method, &xrpcError{Status: resp.StatusCode, Code: e.Error, Message: e.Message})
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// post signs in and creates a post with a link card. It answers with the
// post's at:// URI and its bsky.app URL.
func (b *bluesky) post(ctx context.Context, c blueskyCard, now time.Time) (Syndication, error) {
	var session struct{ AccessJwt, Did, Handle string }
	if err := b.call(ctx, "com.atproto.server.createSession", "", map[string]string{"identifier": b.handle, "password": b.password}, "", &session); err != nil {
		return Syndication{}, err
	}
	external := map[string]any{"uri": c.URL, "title": c.Title, "description": c.Description}
	if c.Thumb != nil {
		var up struct{ Blob json.RawMessage }
		if err := b.call(ctx, "com.atproto.repo.uploadBlob", session.AccessJwt, c.Thumb, c.ThumbType, &up); err != nil {
			// A card without its picture is still worth sending.
			log.Printf("bluesky: thumbnail not uploaded: %v", err)
		} else {
			external["thumb"] = up.Blob
		}
	}
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      blueskyText,
		"createdAt": now.UTC().Format(time.RFC3339),
		"langs":     []string{c.Lang},
		"embed":     map[string]any{"$type": "app.bsky.embed.external", "external": external},
	}
	var created struct{ URI, CID string }
	err := b.call(ctx, "com.atproto.repo.createRecord", session.AccessJwt,
		map[string]any{"repo": session.Did, "collection": "app.bsky.feed.post", "record": record}, "", &created)
	if err != nil {
		return Syndication{}, err
	}
	s := Syndication{URI: created.URI, SentAt: now}
	if i := strings.LastIndex(created.URI, "/"); i >= 0 {
		s.URL = "https://bsky.app/profile/" + session.Did + "/post/" + created.URI[i+1:]
	}
	return s, nil
}

// blueskyCardFor builds the card for a post in its original language: the
// plain URL, which shows each reader the version in their own language.
func (a *App) blueskyCardFor(p Post) blueskyCard {
	v := p.text(p.Lang)
	c := blueskyCard{URL: a.cfg.BaseURL + postURL(&p), Title: v.Title, Description: a.textExcerpt(v), Lang: v.Lang}
	if c.URL == postURL(&p) {
		log.Printf("bluesky: BASE_URL is not set, the card links %s", c.URL)
	}
	c.Thumb, c.ThumbType = a.coverThumb(&p)
	return c
}

// coverThumb reads the largest copy of the post's cover (its cut copy,
// cards.go, when there is one) that the card takes. Covers from other
// sites and SVGs go without a picture.
func (a *App) coverThumb(p *Post) ([]byte, string) {
	im, _, ok := a.postCover(p)
	if !ok {
		return nil, ""
	}
	key := im.Key
	types := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp"}
	ctype := types[strings.ToLower(path.Ext(key))]
	if ctype == "" {
		return nil, ""
	}
	// Widest first, but no wider than a card needs.
	for i := len(im.Widths) - 1; i >= 0; i-- {
		w := im.Widths[i]
		if w > maxImageSide {
			continue
		}
		file := strings.TrimPrefix(variantURL(im.Key, im.Width, w), "/img/")
		data, err := os.ReadFile(filepath.Join(a.imagesDir, filepath.FromSlash(file)))
		if err == nil && len(data) <= blueskyThumbMax {
			return data, ctype
		}
	}
	return nil, ""
}

var errBlueskyOff = errors.New("Bluesky is not set up")

// sendBluesky posts the link once, if the post asks for it and visitors
// can see it. The result or the error is recorded on the post.
func (a *App) sendBluesky(id string) error {
	if a.bsky == nil {
		return errBlueskyOff
	}
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	now := a.now()
	var p Post
	due := false
	a.store.view(func(d *Data) {
		if q := d.post(id); q != nil && q.SendBluesky && q.Bluesky == nil && q.live(now) {
			p, due = *q, true
		}
	})
	if !due {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := a.bsky.post(ctx, a.blueskyCardFor(p), now)
	if err != nil {
		log.Printf("bluesky %s: %v", p.Slug, err)
	} else {
		log.Printf("bluesky %s: sent, %s", p.Slug, s.URL)
	}
	uerr := a.store.update(func(d *Data) error {
		q := d.post(id)
		if q == nil {
			return errNoChange
		}
		if err != nil {
			q.BlueskyError = err.Error()
			return nil
		}
		q.Bluesky, q.BlueskyError = &s, ""
		return nil
	})
	if uerr != nil {
		log.Printf("bluesky %s: recording the result: %v", p.Slug, uerr)
	}
	return err
}

// sendDue sends the posts that asked for Bluesky and have gone live since,
// scheduled ones included. A post whose last try failed waits for the
// admin to save it again, so a broken setup does not retry every minute.
func (a *App) sendDue() {
	if a.bsky == nil {
		return
	}
	now := a.now()
	var ids []string
	a.store.view(func(d *Data) {
		for _, p := range d.Posts {
			if p.SendBluesky && p.Bluesky == nil && p.BlueskyError == "" && p.live(now) {
				ids = append(ids, p.ID)
			}
		}
	})
	for _, id := range ids {
		a.sendBluesky(id)
	}
}

// runSender checks for due posts every minute until ctx ends.
func (a *App) runSender(ctx context.Context) {
	if a.bsky == nil {
		return
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sendDue()
		}
	}
}
