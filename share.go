package main

import (
	"html/template"
	"net/url"
	"strings"
)

// Brand marks from Simple Icons 16.36.0 (CC0), as in social.go.
var (
	iconFacebook = template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M9.101 23.691v-7.98H6.627v-3.667h2.474v-1.58c0-4.085 1.848-5.978 5.858-5.978.401 0 .955.042 1.468.103a8.68 8.68 0 0 1 1.141.195v3.325a8.623 8.623 0 0 0-.653-.036 26.805 26.805 0 0 0-.733-.009c-.707 0-1.259.096-1.675.309a1.686 1.686 0 0 0-.679.622c-.258.42-.374.995-.374 1.752v1.297h3.919l-.386 2.103-.287 1.564h-3.246v8.245C19.396 23.238 24 18.179 24 12.044c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.628 3.874 10.35 9.101 11.647Z"/></svg>`)
	iconTelegram = template.HTML(`<svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"/></svg>`)
	// Drawn for the blog: a chain link and the share mark (a box with an
	// arrow out of it), in lines of the text colour.
	iconLink  = template.HTML(`<svg class="icon line" viewBox="0 0 24 24" aria-hidden="true"><path d="M10 13.5a4.5 4.5 0 0 0 6.4.4l3-3a4.5 4.5 0 0 0-6.3-6.4l-1.6 1.6M14 10.5a4.5 4.5 0 0 0-6.4-.4l-3 3a4.5 4.5 0 0 0 6.3 6.4l1.6-1.6"/></svg>`)
	iconShare = template.HTML(`<svg class="icon line" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 15V3m0 0L8 7m4-4 4 4M8 10H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8a2 2 0 0 0-2-2h-2"/></svg>`)
)

// shareLink opens one network's window for a new post with the link in it.
type shareLink struct {
	Name, URL string
	Icon      template.HTML
}

// sharePost is the "share" box under a post: links to the networks for
// wide screens, and the page's address and title for the copy button and
// for the phone's own share sheet (site.js).
type sharePost struct {
	URL, Title string
	Links      []shareLink
}

// shareFor builds the box for the post at link (absolute, with ?lang= when
// the post has both languages) titled title. Each address is the one the
// network documents, read 2026-11-09:
//   - Bluesky: bsky.app/intent/compose?text= (docs.bsky.app, "Action
//     Intent Links"); the text holds the title and the link.
//   - X: x.com/intent/tweet?text=&url= (docs.x.com, "Post Web Intent").
//   - Facebook: its docs give only /dialog/share, which wants an app id;
//     sharer.php?u= is not documented but answers (asks to sign in, while
//     another name in /sharer/ is a 404).
//   - Telegram: t.me/share/url?url=&text= (core.telegram.org/widgets/share).
func shareFor(link, title string) *sharePost {
	// %20 rather than + for spaces, as the networks' own examples have it.
	q := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return &sharePost{URL: link, Title: title, Links: []shareLink{
		{"Bluesky", "https://bsky.app/intent/compose?text=" + q(title+" "+link), iconBluesky},
		{"X", "https://x.com/intent/tweet?text=" + q(title) + "&url=" + q(link), iconX},
		{"Facebook", "https://www.facebook.com/sharer/sharer.php?u=" + q(link), iconFacebook},
		{"Telegram", "https://t.me/share/url?url=" + q(link) + "&text=" + q(title), iconTelegram},
	}}
}
