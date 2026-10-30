package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pageBase is embedded in every public page's data and read by the layout.
type pageBase struct {
	Lang        string // the page's language: the post's, else the site's
	SiteLang    string // language of the blog's own texts
	Title       string // <title>
	SiteTitle   string
	Description string
	Logo        string
	LogoDark    string
	Nav         []Link
	FooterNav   []Link
	Path        string
	Origin      string
	OGTitle     string
	OGImage     string // absolute URL
	OGType      string
	Admin       bool
	Version     string
	Year        int
}

func (a *App) pageBase(r *http.Request, title string) pageBase {
	var s Settings
	a.store.view(func(d *Data) { s = d.Settings })
	b := pageBase{
		Lang: s.Lang, SiteLang: s.Lang, Title: s.Title, SiteTitle: s.Title, Description: s.Description,
		Logo: s.Logo, LogoDark: s.LogoDark, Nav: s.Nav, FooterNav: s.FooterNav, Path: r.URL.Path, Origin: a.origin(r),
		OGTitle: s.Title, OGType: "website", Admin: a.isAdmin(r), Version: version, Year: a.now().Year(),
	}
	if title != "" {
		b.Title = title + " — " + s.Title
		b.OGTitle = title
	}
	return b
}

type tagLink struct{ Name, URL string }

// card is a post as the list and the post page show it.
type card struct {
	URL, Title, Excerpt, Date, DateISO, Lang string
	Cover                                    string
	CoverSrcset                              string
	CoverW, CoverH                           int
	Tags                                     []tagLink
	Page, Draft, Scheduled                   bool
	ScheduledFor                             string
}

func postURL(p *Post) string { return "/" + p.Slug + "/" }

// toCard copies what the templates need. Call it outside the store lock:
// the cover lookup takes it.
func (a *App) toCard(p Post, now time.Time) card {
	lang := normLang(p.Lang)
	c := card{
		URL: postURL(&p), Title: p.Title, Excerpt: p.Excerpt, Lang: lang,
		Date: formatDate(lang, p.PublishedAt), DateISO: p.PublishedAt.Format(time.RFC3339),
		Cover: p.Cover, Page: p.Page, Draft: p.Draft,
		Scheduled: !p.Draft && p.PublishedAt.After(now),
	}
	if c.Scheduled {
		c.ScheduledFor = formatDate(lang, p.PublishedAt) + " " + p.PublishedAt.Format("15:04")
	}
	for _, tg := range p.Tags {
		c.Tags = append(c.Tags, tagLink{Name: tg, URL: "/tag/" + tagSlug(tg) + "/"})
	}
	if key, ok := strings.CutPrefix(p.Cover, "/img/"); ok {
		if im, ok := a.store.image(key); ok {
			c.CoverW, c.CoverH = im.Width, im.Height
			var set []string
			for _, w := range im.Widths {
				set = append(set, fmt.Sprintf("%s %dw", variantURL(im.Key, im.Width, w), w))
			}
			c.CoverSrcset = strings.Join(set, ", ")
		}
	}
	return c
}

// postExcerpt is the excerpt or the start of the text.
func (a *App) postExcerpt(p Post) string {
	if p.Excerpt != "" {
		return p.Excerpt
	}
	h, err := a.md.render(p.Body)
	if err != nil {
		return ""
	}
	return excerpt(plainText(h), 220)
}

type indexPage struct {
	pageBase
	Posts            []card
	Heading          string
	PrevURL, NextURL string
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	n := 1
	if s := r.PathValue("n"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 2 {
			a.notFound(w, r)
			return
		}
		n = v
	}
	now := a.now()
	var list []Post
	var perPage int
	tagName := ""
	a.store.view(func(d *Data) {
		perPage = d.Settings.PostsPerPage
		for _, p := range d.Posts {
			if p.Page || !p.live(now) || (tag != "" && !p.hasTag(tag)) {
				continue
			}
			if tag != "" && tagName == "" {
				for _, tg := range p.Tags {
					if tagSlug(tg) == tag {
						tagName = tg
					}
				}
			}
			list = append(list, *p)
		}
	})
	if tag != "" && len(list) == 0 {
		a.notFound(w, r)
		return
	}
	start := (n - 1) * perPage
	if start >= len(list) && n > 1 {
		a.notFound(w, r)
		return
	}
	end := min(start+perPage, len(list))
	prefix := "/"
	if tag != "" {
		prefix = "/tag/" + tag + "/"
	}
	data := indexPage{}
	title := ""
	if tag != "" {
		title = tagName
		data.Heading = tagName
	}
	data.pageBase = a.pageBase(r, title)
	if n > 1 {
		data.Title += " — " + tf(data.Lang, "page_n", n)
	}
	for _, p := range list[start:end] {
		c := a.toCard(p, now)
		c.Excerpt = a.postExcerpt(p)
		data.Posts = append(data.Posts, c)
	}
	if n > 2 {
		data.PrevURL = fmt.Sprintf("%spage/%d/", prefix, n-1)
	} else if n == 2 {
		data.PrevURL = prefix
	}
	if end < len(list) {
		data.NextURL = fmt.Sprintf("%spage/%d/", prefix, n+1)
	}
	a.render(w, http.StatusOK, "index", data)
}

type postPage struct {
	pageBase
	Post card
	HTML template.HTML
	// EditURL is set for the signed-in admin.
	EditURL string
}

// handleSlugPath serves /{slug}/ and sends /{slug} there.
func (a *App) handleSlugPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/")
	slug, tail, slash := strings.Cut(rest, "/")
	switch {
	case slug == "" || tail != "":
		a.notFound(w, r)
	case slash:
		a.handlePost(w, r, slug)
	default:
		a.handleSlashless(w, r, slug)
	}
}

func (a *App) handlePost(w http.ResponseWriter, r *http.Request, slug string) {
	now := a.now()
	var p Post
	found := false
	a.store.view(func(d *Data) {
		if q := d.postBySlug(slug); q != nil {
			p, found = *q, true
		}
	})
	admin := a.isAdmin(r)
	if !found || (!p.live(now) && !admin) {
		a.notFound(w, r)
		return
	}
	h, err := a.md.render(p.Body)
	if err != nil {
		log.Printf("render %s: %v", p.Slug, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	data := postPage{pageBase: a.pageBase(r, p.Title), Post: a.toCard(p, now), HTML: template.HTML(h)}
	data.Lang = data.Post.Lang
	data.Description = a.postExcerpt(p)
	data.OGType = "article"
	if p.Cover != "" {
		data.OGImage = absURL(data.Origin, p.Cover)
	}
	if admin {
		data.EditURL = "/admin/posts/" + p.ID
	}
	a.render(w, http.StatusOK, "post", data)
}

// handleSlashless sends /slug to /slug/, as Ghost's links had it.
func (a *App) handleSlashless(w http.ResponseWriter, r *http.Request, slug string) {
	found := false
	a.store.view(func(d *Data) { found = d.postBySlug(slug) != nil })
	if !found {
		a.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/"+slug+"/", http.StatusMovedPermanently)
}

type messagePage struct {
	pageBase
	Text string
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	b := a.pageBase(r, "404")
	a.render(w, http.StatusNotFound, "message", messagePage{pageBase: b, Text: t(b.Lang, "not_found")})
}

func absURL(origin, u string) string {
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		return origin + u
	}
	return u
}
