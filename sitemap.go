package main

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"
)

// handleSitemap lists the home page and every live post and page for
// search engines. A page in both languages is listed under each of its
// addresses, every entry naming all of them as alternates, which is how
// Google reads hreflang in a sitemap.
func (a *App) handleSitemap(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		path string
		mod  time.Time
		both bool
	}
	origin, now := a.origin(r), a.now()
	home := entry{path: "/", both: true}
	var es []entry
	a.store.view(func(d *Data) {
		for _, p := range d.Posts {
			if !p.live(now) {
				continue
			}
			// A scheduled post was last saved before it went live.
			mod := p.UpdatedAt
			if mod.Before(p.PublishedAt) {
				mod = p.PublishedAt
			}
			es = append(es, entry{postURL(p), mod, p.Translation != nil})
			if !p.Page && mod.After(home.mod) {
				home.mod = mod
			}
		}
	})
	es = append([]entry{home}, es...)

	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	esc := func(s string) string {
		var sb strings.Builder
		xml.EscapeText(&sb, []byte(s))
		return sb.String()
	}
	for _, e := range es {
		u := origin + e.path
		mod := ""
		if !e.mod.IsZero() {
			mod = "<lastmod>" + e.mod.UTC().Format("2006-01-02") + "</lastmod>"
		}
		if !e.both {
			b.WriteString("<url><loc>" + esc(u) + "</loc>" + mod + "</url>\n")
			continue
		}
		alts := langAlternates(u)
		var links strings.Builder
		for _, al := range alts {
			links.WriteString(`<xhtml:link rel="alternate" hreflang="` + al.Lang + `" href="` + esc(al.URL) + `"/>`)
		}
		for _, al := range alts {
			b.WriteString("<url><loc>" + esc(al.URL) + "</loc>" + mod + links.String() + "</url>\n")
		}
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(b.String()))
}
