package main

import (
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pageBase is embedded in every public page's data and read by the layout.
type pageBase struct {
	Lang        string // the page's language: the post's, else SiteLang
	SiteLang    string // language of the blog's own texts: the reader's
	Title       string // <title>
	SiteTitle   string
	Description string
	Logo        string
	LogoDark    string
	Nav         []Link
	FooterNav   []Link
	// Mastodon is the profile linked in the footer with rel="me", which
	// lets Mastodon show the blog as verified on that profile.
	Mastodon string
	// Creator is Mastodon as @user@server for the fediverse:creator tag,
	// which credits the profile on Mastodon's link previews.
	Creator string
	Path    string
	Origin  string
	FeedURL string // the feed in SiteLang
	OGTitle string
	OGImage string // absolute URL
	// OGImageW and OGImageH are the cut cover's size (cards.go), 0 when
	// og:image is the cover as uploaded.
	OGImageW, OGImageH int
	OGType             string
	Admin              bool
	Version            string
	Year               int
	// Gallery loads the picture viewer: the page has images that open in it.
	Gallery bool
	// Canonical and Alternates tell search engines which address holds
	// which language (see setLangURLs).
	Canonical  string
	Alternates []alternate
}

// alternate is a language version of a page: Lang is en, ru or x-default.
type alternate struct{ Lang, URL string }

// setLangURLs points search engines at the page's language versions. A
// page in both languages has one address per language, ?lang=en and
// ?lang=ru, plus the bare path as x-default, whose language follows the
// reader: crawlers send no Accept-Language, so without these links they
// would only ever see one language. A page in one language is canonical
// at the bare path.
func (b *pageBase) setLangURLs(r *http.Request, both bool) {
	u := b.Origin + b.Path
	b.Canonical, b.Alternates = u, nil
	if !both {
		return
	}
	if l := r.URL.Query().Get("lang"); l == "ru" || l == "en" {
		b.Canonical = u + "?lang=" + l
	}
	b.Alternates = langAlternates(u)
}

func langAlternates(u string) []alternate {
	return []alternate{{"en", u + "?lang=en"}, {"ru", u + "?lang=ru"}, {"x-default", u}}
}

func (a *App) pageBase(r *http.Request, title string) pageBase {
	var s Settings
	a.store.view(func(d *Data) { s = d.Settings })
	lang := readerLang(r, s.Lang)
	b := pageBase{
		Lang: lang, SiteLang: lang, SiteTitle: s.Title, Description: pickText(lang, s.Lang, s.Description, s.DescriptionAlt),
		Logo: s.Logo, LogoDark: s.LogoDark, Nav: localLinks(s.Nav, lang, s.Lang), FooterNav: localLinks(s.FooterNav, lang, s.Lang), Mastodon: s.Mastodon,
		Path: r.URL.Path, Origin: a.origin(r), FeedURL: "/rss/", OGType: "website", Admin: a.isAdmin(r), Version: version, Year: a.now().Year(),
	}
	b.Creator = fediverseHandle(s.Mastodon)
	if lang != normLang(s.Lang) {
		b.FeedURL = "/rss/" + lang + "/"
	}
	b.setTitle(title)
	return b
}

func (b *pageBase) setTitle(title string) {
	b.Title, b.OGTitle = b.SiteTitle, b.SiteTitle
	if title != "" {
		b.Title = title + " — " + b.SiteTitle
		b.OGTitle = title
	}
}

// localLinks puts each link's label in lang.
func localLinks(ls []Link, lang, defaultLang string) []Link {
	out := make([]Link, len(ls))
	for i, l := range ls {
		out[i] = Link{Label: pickText(lang, defaultLang, l.Label, l.LabelAlt), URL: l.URL}
	}
	return out
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

// toCard copies what the templates need, with the text in v and dates in
// uiLang. Call it outside the store lock: the cover lookup takes it.
func (a *App) toCard(p Post, v postText, uiLang string, now time.Time) card {
	c := card{
		URL: postURL(&p), Title: v.Title, Excerpt: v.Excerpt, Lang: v.Lang,
		Date: formatDate(uiLang, p.PublishedAt), DateISO: p.PublishedAt.Format(time.RFC3339),
		Cover: p.Cover, Page: p.Page, Draft: p.Draft,
		Scheduled: !p.Draft && p.PublishedAt.After(now),
	}
	if c.Scheduled {
		c.ScheduledFor = formatDate(uiLang, p.PublishedAt) + " " + p.PublishedAt.Format("15:04")
	}
	for _, tg := range p.Tags {
		c.Tags = append(c.Tags, tagLink{Name: tg, URL: "/tag/" + tagSlug(tg) + "/"})
	}
	if im, _, ok := a.postCover(&p); ok {
		c.Cover = "/img/" + im.Key
		c.CoverW, c.CoverH, c.CoverSrcset = im.Width, im.Height, srcset(im)
	}
	return c
}

// textExcerpt is the excerpt or the start of the text.
func (a *App) textExcerpt(v postText) string {
	if v.Excerpt != "" {
		return v.Excerpt
	}
	h, err := a.md.render(v.Body)
	if err != nil {
		return ""
	}
	return excerpt(plainText(h), 220)
}

type indexPage struct {
	pageBase
	// Featured is the newest post, on the first page of a list only; Posts
	// are the rest, shown as tiles.
	Featured         *card
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
	// The list's own texts and the post titles come in both languages.
	data.setLangURLs(r, true)
	if n > 1 {
		data.Title += " — " + tf(data.SiteLang, "page_n", n)
	}
	for _, p := range list[start:end] {
		v := p.text(data.SiteLang)
		c := a.toCard(p, v, data.SiteLang, now)
		c.Excerpt = a.textExcerpt(v)
		if n == 1 && data.Featured == nil {
			data.Featured = &c
			continue
		}
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
	// OnlyIn is set when the reader's language has no version: the
	// message key saying which language the text is in.
	OnlyIn string
	// AltURL links the version in AltLang, for posts that have both.
	AltURL, AltLang string
	// Older and Newer are the neighbouring posts, for a live post only.
	Older, Newer *card
	// EditURL is set for the signed-in admin.
	EditURL string
	// Social lists the profiles for the box under a post.
	Social []socialLink
	// CoverZoom is the cover link's attributes for the gallery. It is
	// written in Go: html/template would treat data-pswp-srcset, a name
	// with "src" in it, as a URL and escape its spaces and commas.
	CoverZoom template.HTMLAttr
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

// movedPost is where a post merged into another one went, or "".
func (a *App) movedPost(slug string) string {
	var to string
	a.store.view(func(d *Data) {
		if d.postBySlug(slug) == nil {
			to = d.SlugRedirects[slug]
		}
	})
	return to
}

func (a *App) handlePost(w http.ResponseWriter, r *http.Request, slug string) {
	if to := a.movedPost(slug); to != "" {
		http.Redirect(w, r, to, http.StatusMovedPermanently)
		return
	}
	now := a.now()
	var p Post
	var older, newer *Post
	found := false
	a.store.view(func(d *Data) {
		if q := d.postBySlug(slug); q != nil {
			p, found = *q, true
			older, newer = d.neighbours(q, now)
		}
	})
	admin := a.isAdmin(r)
	if !found || (!p.live(now) && !admin) {
		a.notFound(w, r)
		return
	}
	data := postPage{pageBase: a.pageBase(r, "")}
	data.setLangURLs(r, p.Translation != nil)
	lang := data.SiteLang
	v := p.text(lang)
	h, err := a.md.render(v.Body)
	if err != nil {
		log.Printf("render %s: %v", p.Slug, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	data.setTitle(v.Title)
	data.Post, data.HTML = a.toCard(p, v, lang, now), template.HTML(h)
	data.Lang = v.Lang
	if v.Lang != lang {
		data.OnlyIn = "only_" + v.Lang
	}
	if p.Translation != nil {
		data.AltLang = otherLang(v.Lang)
		data.AltURL = postURL(&p) + "?lang=" + data.AltLang
	}
	data.Description = a.textExcerpt(v)
	data.OGType = "article"
	if p.Cover != "" {
		data.OGImage = absURL(data.Origin, p.Cover)
		if im, cut, _ := a.postCover(&p); cut {
			data.OGImage, data.OGImageW, data.OGImageH = absURL(data.Origin, "/img/"+im.Key), im.Width, im.Height
		}
	}
	if older != nil {
		c := a.toCard(*older, older.text(lang), lang, now)
		c.Excerpt = a.textExcerpt(older.text(lang))
		data.Older = &c
	}
	if newer != nil {
		c := a.toCard(*newer, newer.text(lang), lang, now)
		c.Excerpt = a.textExcerpt(newer.text(lang))
		data.Newer = &c
	}
	if admin {
		data.EditURL = "/admin/posts/" + p.ID
	}
	if !p.Page {
		a.store.view(func(d *Data) { data.Social = socialLinks(d.Settings, lang) })
	}
	if c := data.Post; c.CoverW > 0 {
		esc := html.EscapeString
		attrs := fmt.Sprintf(` href="%s" data-pswp-width="%d" data-pswp-height="%d"`, esc(c.Cover), c.CoverW, c.CoverH)
		if c.CoverSrcset != "" {
			attrs += fmt.Sprintf(` data-pswp-srcset="%s"`, esc(c.CoverSrcset))
		}
		data.CoverZoom = template.HTMLAttr(attrs)
	}
	data.Gallery = data.CoverZoom != "" || strings.Contains(string(data.HTML), `<a class="zoom"`)
	a.render(w, http.StatusOK, "post", data)
}

// handleSlashless sends /slug to /slug/, as Ghost's links had it.
func (a *App) handleSlashless(w http.ResponseWriter, r *http.Request, slug string) {
	if to := a.movedPost(slug); to != "" {
		http.Redirect(w, r, to, http.StatusMovedPermanently)
		return
	}
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
	a.render(w, http.StatusNotFound, "message", messagePage{pageBase: b, Text: t(b.SiteLang, "not_found")})
}

func absURL(origin, u string) string {
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		return origin + u
	}
	return u
}
