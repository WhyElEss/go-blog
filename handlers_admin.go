package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// adminBase is embedded in every admin page's data.
type adminBase struct {
	Lang      string
	Title     string
	SiteTitle string
	Path      string
	Flash     string
	Error     string
	Version   string
	NoNav     bool
}

func (a *App) adminBase(r *http.Request, title string) adminBase {
	lang := detectLang(r)
	var site string
	a.store.view(func(d *Data) { site = d.Settings.Title })
	b := adminBase{Lang: lang, Title: title, SiteTitle: site, Path: r.URL.Path, Version: version}
	if m := r.URL.Query().Get("m"); m != "" {
		if _, ok := messages[m]; ok {
			if strings.HasSuffix(m, "_failed") {
				b.Error = t(lang, m)
			} else {
				b.Flash = t(lang, m)
			}
		}
	}
	return b
}

// handleLang serves the RU/EN switches, /lang/{code} on the site and
// /admin/lang/{code}: one cookie for both.
func (a *App) handleLang(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if code != "ru" && code != "en" {
		code = "en"
	}
	http.SetCookie(w, &http.Cookie{Name: "lang", Value: code, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		next = "/"
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			next = "/admin/"
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if a.isAdmin(r) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	b := a.adminBase(r, t(detectLang(r), "login"))
	b.NoNav = true
	a.render(w, http.StatusOK, "admin_login", b)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	lang := detectLang(r)
	b := a.adminBase(r, t(lang, "login"))
	b.NoNav = true
	if !a.loginLimit.allow(a.clientIP(r)) {
		b.Error = t(lang, "too_many")
		a.render(w, http.StatusTooManyRequests, "admin_login", b)
		return
	}
	if !a.adminHash.verify(r.FormValue("password")) {
		b.Error = t(lang, "bad_password")
		a.render(w, http.StatusUnauthorized, "admin_login", b)
		return
	}
	exp := a.now().Add(sessionMaxAge)
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: a.signSession(exp), Path: "/", Expires: exp,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

type adminRow struct {
	ID, Title, URL, Date, Status, StatusKey string
	Langs                                   string // "EN", "EN · RU"
}

type postsPage struct {
	adminBase
	Posts, Pages []adminRow
}

func (a *App) handleAdminPosts(w http.ResponseWriter, r *http.Request) {
	b := a.adminBase(r, t(detectLang(r), "posts"))
	now := a.now()
	data := postsPage{adminBase: b}
	a.store.view(func(d *Data) {
		for _, p := range d.Posts {
			row := adminRow{ID: p.ID, Title: p.Title, URL: postURL(p), Langs: strings.ToUpper(normLang(p.Lang))}
			if p.Translation != nil {
				row.Langs += " · " + strings.ToUpper(otherLang(p.Lang))
			}
			if !p.PublishedAt.IsZero() {
				row.Date = p.PublishedAt.In(time.Local).Format("2006-01-02 15:04")
			}
			switch {
			case p.Draft:
				row.StatusKey = "st_draft"
			case p.PublishedAt.After(now):
				row.StatusKey = "st_scheduled"
			default:
				row.StatusKey = "st_published"
			}
			row.Status = t(b.Lang, row.StatusKey)
			if p.Page {
				data.Pages = append(data.Pages, row)
			} else {
				data.Posts = append(data.Posts, row)
			}
		}
	})
	draftsFirst(data.Posts)
	draftsFirst(data.Pages)
	a.render(w, http.StatusOK, "admin_posts", data)
}

type editPage struct {
	adminBase
	Post      Post
	BskyOn    bool      // Bluesky is set up
	BskySent  string    // when the link went out, in the admin's words
	Tabs      []editTab // the original language first
	New       bool
	TagsText  string
	DateValue string // datetime-local
	Live      bool
}

// editTab is the editor's text fields for one language.
type editTab struct {
	Lang, Name, Title, Body, Excerpt string
}

// editorLangs lists the blog's languages in the editor's tab order.
var editorLangs = []string{"en", "ru"}

func (a *App) editData(r *http.Request, p Post, isNew bool) editPage {
	lang := detectLang(r)
	title := t(lang, "edit_post")
	if isNew {
		title = t(lang, "new_post")
		if p.Page {
			title = t(lang, "new_page")
		}
	}
	e := editPage{adminBase: a.adminBase(r, title), Post: p, New: isNew, TagsText: strings.Join(p.Tags, ", "), Live: p.live(a.now())}
	e.Post.Lang = normLang(p.Lang)
	e.BskyOn = a.bsky != nil
	if p.Bluesky != nil {
		e.BskySent = formatDate(e.Lang, p.Bluesky.SentAt.In(time.Local)) + " " + p.Bluesky.SentAt.In(time.Local).Format("15:04")
	}
	for _, l := range []string{e.Post.Lang, otherLang(e.Post.Lang)} {
		tab := editTab{Lang: l, Name: t(l, "lang_name")}
		switch {
		case l == e.Post.Lang:
			tab.Title, tab.Body, tab.Excerpt = p.Title, p.Body, p.Excerpt
		case p.Translation != nil:
			tab.Title, tab.Body, tab.Excerpt = p.Translation.Title, p.Translation.Body, p.Translation.Excerpt
		}
		e.Tabs = append(e.Tabs, tab)
	}
	if !p.PublishedAt.IsZero() {
		e.DateValue = p.PublishedAt.In(time.Local).Format("2006-01-02T15:04")
	}
	return e
}

func (a *App) handleNewForm(w http.ResponseWriter, r *http.Request) {
	var lang string
	a.store.view(func(d *Data) { lang = d.Settings.Lang })
	p := Post{Draft: true, Lang: lang, Page: r.URL.Query().Get("page") == "1"}
	a.render(w, http.StatusOK, "admin_edit", a.editData(r, p, true))
}

func (a *App) handleEditForm(w http.ResponseWriter, r *http.Request) {
	var p Post
	found := false
	a.store.view(func(d *Data) {
		if q := d.post(r.PathValue("id")); q != nil {
			p, found = *q, true
		}
	})
	if !found {
		http.NotFound(w, r)
		return
	}
	a.render(w, http.StatusOK, "admin_edit", a.editData(r, p, false))
}

func splitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		tg := strings.TrimSpace(part)
		if tg == "" || seen[tagSlug(tg)] {
			continue
		}
		seen[tagSlug(tg)] = true
		out = append(out, tg)
	}
	return out
}

// handleSave creates (no id) or updates a post from the editor form. The
// action button decides the draft state: publish, unpublish or keep.
func (a *App) handleSave(w http.ResponseWriter, r *http.Request) {
	lang := detectLang(r)
	id := r.PathValue("id")
	now := a.now()
	in := Post{
		Slug:  slugify(r.FormValue("slug")),
		Cover: strings.TrimSpace(r.FormValue("cover")),
		Tags:  splitTags(r.FormValue("tags")),
		Page:  r.FormValue("page") == "1",
		Lang:  normLang(r.FormValue("lang")),

		SendBluesky: r.FormValue("bluesky") == "1",
	}
	// One set of text fields per language; the original language's is the
	// post, the other's (when filled in) its translation. A post written
	// only in the tab of the other language is in that language.
	texts := map[string]Translation{}
	for _, l := range editorLangs {
		texts[l] = Translation{
			Title:   strings.TrimSpace(r.FormValue("title_" + l)),
			Body:    strings.ReplaceAll(r.FormValue("body_"+l), "\r\n", "\n"),
			Excerpt: strings.TrimSpace(r.FormValue("excerpt_" + l)),
		}
	}
	empty := func(tr Translation) bool {
		return tr.Title == "" && strings.TrimSpace(tr.Body) == "" && tr.Excerpt == ""
	}
	if empty(texts[in.Lang]) && !empty(texts[otherLang(in.Lang)]) {
		in.Lang = otherLang(in.Lang)
	}
	main, alt := texts[in.Lang], texts[otherLang(in.Lang)]
	in.Title, in.Body, in.Excerpt = main.Title, main.Body, main.Excerpt
	if !empty(alt) {
		in.Translation = &alt
	}
	if in.Slug == "" {
		in.Slug = slugify(in.Title)
	}
	if in.Slug == "" {
		in.Slug = "post-" + randomString(4)
	}
	var date time.Time
	if s := strings.TrimSpace(r.FormValue("published_at")); s != "" {
		d, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local)
		if err != nil {
			a.saveError(w, r, in, id, t(lang, "err_date"))
			return
		}
		date = d
	}
	action := r.FormValue("action")
	if in.Title == "" {
		a.saveError(w, r, in, id, t(lang, "err_title"))
		return
	}
	if in.Translation != nil && in.Translation.Title == "" {
		a.saveError(w, r, in, id, t(lang, "err_title_tr"))
		return
	}
	if reservedSlugs[in.Slug] {
		a.saveError(w, r, in, id, t(lang, "err_slug"))
		return
	}

	var saved *Post
	err := a.store.update(func(d *Data) error {
		if other := d.postBySlug(in.Slug); other != nil && other.ID != id {
			return errSlugTaken
		}
		p := &Post{}
		if id != "" {
			p = d.post(id)
			if p == nil {
				return errNotFound
			}
		}
		draft := p.Draft
		if id == "" {
			draft = true
		}
		switch action {
		case "publish":
			draft = false
		case "unpublish":
			draft = true
		}
		// No date typed: drafts have none, a first publication gets now,
		// a published post keeps its date.
		switch {
		case !date.IsZero():
			p.PublishedAt = date
		case draft:
			p.PublishedAt = time.Time{}
		case p.PublishedAt.IsZero() || p.Draft:
			p.PublishedAt = now.Truncate(time.Minute)
		}
		p.Title, p.Slug, p.Body, p.Excerpt, p.Cover, p.Tags, p.Page, p.Lang = in.Title, in.Slug, in.Body, in.Excerpt, in.Cover, in.Tags, in.Page, in.Lang
		p.Translation = in.Translation
		// Once sent, the link stays sent. Ticking the box again after a
		// failure asks for another try.
		if p.Bluesky == nil {
			if in.SendBluesky && (!p.SendBluesky || p.BlueskyError != "") {
				p.BlueskyError = ""
			}
			p.SendBluesky = in.SendBluesky
		}
		p.Draft = draft
		p.UpdatedAt = now
		if id == "" {
			p.ID = newID()
			p.CreatedAt = now
			d.Posts = append(d.Posts, p)
		}
		saved = p
		return nil
	})
	switch {
	case errors.Is(err, errSlugTaken):
		a.saveError(w, r, in, id, t(lang, "err_slug"))
		return
	case errors.Is(err, errNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		log.Printf("save post: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	msg := "saved"
	if a.bsky != nil {
		var due bool
		a.store.view(func(d *Data) {
			if p := d.post(saved.ID); p != nil {
				due = p.SendBluesky && p.Bluesky == nil && p.BlueskyError == "" && p.live(a.now())
			}
		})
		if due {
			if err := a.sendBluesky(saved.ID); err != nil {
				msg = "bsky_failed"
			} else {
				msg = "bsky_sent"
			}
		}
	}
	http.Redirect(w, r, "/admin/posts/"+saved.ID+"?m="+msg, http.StatusSeeOther)
}

var errSlugTaken = errors.New("slug taken")

// saveError shows the editor again with what was typed and the error.
func (a *App) saveError(w http.ResponseWriter, r *http.Request, in Post, id string, msg string) {
	if id != "" {
		a.store.view(func(d *Data) {
			if p := d.post(id); p != nil {
				in.ID, in.Draft, in.PublishedAt, in.CreatedAt = p.ID, p.Draft, p.PublishedAt, p.CreatedAt
				in.Bluesky, in.BlueskyError = p.Bluesky, p.BlueskyError
			}
		})
	} else {
		in.Draft = true
	}
	e := a.editData(r, in, id == "")
	if s := r.FormValue("published_at"); s != "" {
		e.DateValue = s
	}
	e.Error = msg
	a.render(w, http.StatusUnprocessableEntity, "admin_edit", e)
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.store.update(func(d *Data) error {
		for i, p := range d.Posts {
			if p.ID == id {
				d.Posts = append(d.Posts[:i], d.Posts[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil && !errors.Is(err, errNotFound) {
		log.Printf("delete post: %v", err)
	}
	http.Redirect(w, r, "/admin/?m=deleted", http.StatusSeeOther)
}

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	h, err := a.md.render(r.FormValue("body"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, h)
}

const maxUpload = 40 << 20

// handleUpload stores one image (form field "file") and answers with its
// URL and size as JSON.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "no file: "+err.Error())
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(data) > maxUpload {
		jsonError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file larger than %d MB", maxUpload>>20))
		return
	}
	im, err := a.addImage(hdr.Filename, data, a.now())
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"url": "/img/" + im.Key, "width": im.Width, "height": im.Height})
}

// addImage processes an upload and records it. Holding the store lock
// while resizing would stall the site, so the name is reserved by checking
// the files on disk and the records, then the record is added.
func (a *App) addImage(name string, data []byte, when time.Time) (*Image, error) {
	exists := func(key string) bool {
		_, known := a.store.image(key)
		return known || fileExists(a.imagesDir, key)
	}
	a.uploadMu.Lock()
	defer a.uploadMu.Unlock()
	im, err := processImage(a.imagesDir, name, data, when, exists)
	if err != nil {
		return nil, err
	}
	im.Uploaded = a.now()
	err = a.store.update(func(d *Data) error {
		d.Images[im.Key] = im
		return nil
	})
	if err != nil {
		removeImageFiles(a.imagesDir, im)
		return nil, err
	}
	return im, nil
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

type imageRow struct {
	Image
	URL, Thumb, Markdown, Size string
	Uses                       int
}

type imagesPage struct {
	adminBase
	Images []imageRow
}

func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	data := imagesPage{adminBase: a.adminBase(r, t(detectLang(r), "images"))}
	if r.URL.Query().Get("m") == "img_in_use" {
		data.Flash, data.Error = "", t(data.Lang, "img_in_use")
	}
	a.store.view(func(d *Data) {
		for _, im := range d.Images {
			data.Images = append(data.Images, imageRow{
				Image: *im, URL: "/img/" + im.Key, Thumb: variantURL(im.Key, im.Width, im.Widths[0]),
				Markdown: "![](/img/" + im.Key + ")", Size: humanBytes(im.Bytes), Uses: d.imageUses(im.Key),
			})
		}
	})
	sortImages(data.Images)
	a.render(w, http.StatusOK, "admin_images", data)
}

func (a *App) handleImageDelete(w http.ResponseWriter, r *http.Request) {
	key := r.FormValue("key")
	var removed *Image
	inUse := false
	err := a.store.update(func(d *Data) error {
		im := d.Images[key]
		if im == nil {
			return errNoChange
		}
		if d.imageUses(key) > 0 {
			inUse = true
			return errNoChange
		}
		delete(d.Images, key)
		removed = im
		return nil
	})
	if err != nil {
		log.Printf("delete image: %v", err)
	}
	if inUse {
		http.Redirect(w, r, "/admin/images?m=img_in_use", http.StatusSeeOther)
		return
	}
	if removed != nil {
		removeImageFiles(a.imagesDir, removed)
	}
	http.Redirect(w, r, "/admin/images?m=deleted", http.StatusSeeOther)
}

type settingsPage struct {
	adminBase
	S                  Settings
	NavText, FooterTxt string
}

func linksText(ls []Link) string {
	var b strings.Builder
	for _, l := range ls {
		if l.LabelAlt != "" {
			fmt.Fprintf(&b, "%s | %s | %s\n", l.Label, l.LabelAlt, l.URL)
		} else {
			fmt.Fprintf(&b, "%s | %s\n", l.Label, l.URL)
		}
	}
	return b.String()
}

// parseLinks reads "Label | /url/" or "Label | Translated label | /url/"
// lines.
func parseLinks(s string) []Link {
	var out []Link
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Split(line, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		var label, alt, u string
		switch len(parts) {
		case 2:
			label, u = parts[0], parts[1]
		case 3:
			label, alt, u = parts[0], parts[1], parts[2]
		}
		if label == "" || u == "" {
			continue
		}
		if pu, err := url.Parse(u); err != nil || (pu.Scheme != "" && pu.Scheme != "https" && pu.Scheme != "http" && pu.Scheme != "mailto") {
			continue
		}
		out = append(out, Link{Label: label, LabelAlt: alt, URL: u})
	}
	return out
}

func (a *App) handleSettingsForm(w http.ResponseWriter, r *http.Request) {
	data := settingsPage{adminBase: a.adminBase(r, t(detectLang(r), "settings"))}
	a.store.view(func(d *Data) { data.S = d.Settings })
	data.NavText, data.FooterTxt = linksText(data.S.Nav), linksText(data.S.FooterNav)
	a.render(w, http.StatusOK, "admin_settings", data)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	per, _ := strconv.Atoi(r.FormValue("posts_per_page"))
	if per < 1 || per > 100 {
		per = 10
	}
	s := Settings{
		Title:          strings.TrimSpace(r.FormValue("title")),
		Description:    strings.TrimSpace(r.FormValue("description")),
		DescriptionAlt: strings.TrimSpace(r.FormValue("description_alt")),
		Lang:           normLang(r.FormValue("lang")),
		Nav:            parseLinks(r.FormValue("nav")),
		FooterNav:      parseLinks(r.FormValue("footer_nav")),
		Logo:           strings.TrimSpace(r.FormValue("logo")),
		LogoDark:       strings.TrimSpace(r.FormValue("logo_dark")),
		Icon:           strings.TrimSpace(r.FormValue("icon")),
		PostsPerPage:   per,
	}
	if s.Title == "" {
		s.Title = "Blog"
	}
	if err := a.store.update(func(d *Data) error { d.Settings = s; return nil }); err != nil {
		log.Printf("settings: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/settings?m=saved", http.StatusSeeOther)
}

// draftsFirst moves drafts above the rest, keeping the date order.
func draftsFirst(rows []adminRow) {
	slices.SortStableFunc(rows, func(a, b adminRow) int {
		da, db := a.StatusKey == "st_draft", b.StatusKey == "st_draft"
		switch {
		case da == db:
			return 0
		case da:
			return -1
		}
		return 1
	})
}

// sortImages shows the newest uploads first.
func sortImages(rows []imageRow) {
	slices.SortFunc(rows, func(a, b imageRow) int {
		if c := b.Uploaded.Compare(a.Uploaded); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func fileExists(dir, key string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	return err == nil
}
