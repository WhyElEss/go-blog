package main

import (
	"encoding/xml"
	"log"
	"net/http"
	"regexp"
	"time"
)

const feedSize = 20

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Content string     `xml:"xmlns:content,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Self          atomLink  `xml:"atom:link"`
	Items         []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Categories  []string `xml:"category"`
	Description string   `xml:"description"`
	Encoded     cdata    `xml:"content:encoded"`
}

type rssGUID struct {
	Value     string `xml:",chardata"`
	Permalink bool   `xml:"isPermaLink,attr"`
}

type cdata struct {
	Value string `xml:",cdata"`
}

// rootRelative finds src="/…" and href="/…" so feed readers get absolute
// links; the site's own pages use root-relative ones.
var rootRelative = regexp.MustCompile(`(\s(?:src|href|srcset)=")/([^/"])`)

// handleRSS serves /rss/ in the default language and /rss/{ru,en}/. Each
// post is in the feed's language when it has that version, else in its own.
func (a *App) handleRSS(w http.ResponseWriter, r *http.Request) {
	origin := a.origin(r)
	now := a.now()
	code := r.PathValue("lang")
	if code != "" && code != "ru" && code != "en" {
		a.notFound(w, r)
		return
	}
	var s Settings
	var posts []Post
	a.store.view(func(d *Data) {
		s = d.Settings
		for _, p := range d.Posts {
			if !p.Page && p.live(now) {
				posts = append(posts, *p)
				if len(posts) == feedSize {
					break
				}
			}
		}
	})
	lang, self := normLang(s.Lang), "/rss/"
	if code != "" {
		lang, self = code, "/rss/"+code+"/"
	}
	ch := rssChannel{
		Title: s.Title, Link: origin + "/", Description: pickText(lang, s.Lang, s.Description, s.DescriptionAlt), Language: lang,
		Self: atomLink{Href: origin + self, Rel: "self", Type: "application/rss+xml"},
	}
	for _, p := range posts {
		v := p.text(lang)
		h, err := a.md.render(v.Body)
		if err != nil {
			log.Printf("rss render %s: %v", p.Slug, err)
			continue
		}
		h = rootRelative.ReplaceAllString(h, "${1}"+origin+"/${2}")
		// srcset lists several URLs; the regexp above fixes only the first.
		h = srcsetFix.ReplaceAllString(h, "${1}"+origin+"/img/")
		// The translation gets its own link, so readers subscribed to both
		// feeds see two items; the original keeps the plain URL it always had.
		link := origin + postURL(&p)
		if v.Lang != normLang(p.Lang) {
			link += "?lang=" + v.Lang
		}
		it := rssItem{
			Title: v.Title, Link: link, GUID: rssGUID{Value: link, Permalink: true},
			PubDate: p.PublishedAt.Format(time.RFC1123Z), Categories: p.Tags,
			Description: a.textExcerpt(v), Encoded: cdata{h},
		}
		if p.Cover != "" {
			cover := p.Cover
			if im, _, ok := a.postCover(&p); ok {
				cover = imageURL(im, im.Width)
			}
			it.Encoded.Value = `<p><img src="` + absURL(origin, cover) + `" alt=""></p>` + it.Encoded.Value
		}
		ch.Items = append(ch.Items, it)
		if ch.LastBuildDate == "" {
			ch.LastBuildDate = p.UpdatedAt.Format(time.RFC1123Z)
		}
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(rssFeed{Version: "2.0", Content: "http://purl.org/rss/1.0/modules/content/", Atom: "http://www.w3.org/2005/Atom", Channel: ch}); err != nil {
		log.Printf("rss: %v", err)
	}
}

var srcsetFix = regexp.MustCompile(`(, )/img/`)
