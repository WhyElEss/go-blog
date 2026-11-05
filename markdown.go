package main

import (
	"bytes"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Markdown dialect, on top of CommonMark + GitHub tables, strikethrough,
// task lists and bare-URL links:
//
//   - An image alone in its paragraph becomes a figure. The URL fragment
//     places it: ![alt](/img/a.jpg#right) floats right of the following text,
//     #left floats left, #center (or nothing) sits centred across the column.
//     The image title becomes the caption: ![alt](/img/a.jpg#left "Caption").
//   - [gallery] on the first line of a paragraph, with only images on the
//     lines under it, lays them out as a gallery: rows of tiles of one
//     height, each as wide as its picture's proportions ask. A blank line
//     ends it.
//   - A YouTube link alone in its paragraph becomes an embedded player, and
//     so does an Apple Music album, playlist or song link.
//   - Headings take explicit anchors: ## Title {#anchor}. Others get one
//     made from their text.
//   - Footnotes: text[^1] and, on a line of its own, [^1]: The note.
//   - ==text== highlights; [TOC] on its own line lists the headings below
//     it (markdown_ext.go).
//   - Raw HTML passes through: only the admin writes posts.

var (
	kindFigure  = ast.NewNodeKind("Figure")
	kindEmbed   = ast.NewNodeKind("Embed")
	kindGallery = ast.NewNodeKind("Gallery")
)

type galleryNode struct {
	ast.BaseBlock
	items []galleryItem
}

type galleryItem struct{ src, alt, title string }

func (n *galleryNode) Kind() ast.NodeKind { return kindGallery }
func (n *galleryNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}

// galleryMark is the first line of a gallery paragraph.
const galleryMark = "[gallery]"

type figureNode struct {
	ast.BaseBlock
	src, alt, caption, align string
}

func (n *figureNode) Kind() ast.NodeKind { return kindFigure }
func (n *figureNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"src": n.src, "align": n.align}, nil)
}

type embedNode struct {
	ast.BaseBlock
	video string // YouTube video ID
	music string // Apple Music player URL, on embed.music.apple.com
	song  bool   // the music player is a single song, a short one
}

func (n *embedNode) Kind() ast.NodeKind { return kindEmbed }
func (n *embedNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"video": n.video, "music": n.music}, nil)
}

// splitAlign takes the placement fragment off an image URL.
func splitAlign(dest string) (src, align string) {
	base, frag, ok := strings.Cut(dest, "#")
	if ok {
		switch frag {
		case "left", "right", "center":
			return base, frag
		}
	}
	return dest, "center"
}

var youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

var appleMusicPath = regexp.MustCompile(`^/[a-z]{2}/(album|playlist|song)/[^/]+/[A-Za-z0-9._-]+$`)

// appleMusic turns an Apple Music album, playlist or song link
// (music.apple.com/ca/album/name/123, a song in it with ?i=456) into the
// address of its player, and says whether that is a single song.
func appleMusic(raw string) (player string, song bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "music.apple.com") {
		return "", false
	}
	m := appleMusicPath.FindStringSubmatch(u.EscapedPath())
	if m == nil {
		return "", false
	}
	player = "https://embed.music.apple.com" + u.EscapedPath()
	song = m[1] == "song"
	if i := u.Query().Get("i"); i != "" && m[1] == "album" {
		if _, err := strconv.ParseUint(i, 10, 64); err != nil {
			return "", false
		}
		player += "?i=" + i
		song = true
	}
	return player, song
}

// youtubeVideo returns the video ID of a YouTube watch, short or embed URL.
func youtubeVideo(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	host = strings.TrimPrefix(host, "m.")
	var id string
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "youtube-nocookie.com":
		switch {
		case u.Path == "/watch":
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/embed/"), strings.HasPrefix(u.Path, "/shorts/"), strings.HasPrefix(u.Path, "/live/"):
			id = u.Path[strings.LastIndex(u.Path, "/")+1:]
		}
	}
	if youtubeID.MatchString(id) {
		return id
	}
	return ""
}

// nodeText is the plain text inside an inline node (an image's alt text).
func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func blankText(n ast.Node, src []byte) bool {
	t, ok := n.(*ast.Text)
	return ok && len(bytes.TrimSpace(t.Segment.Value(src))) == 0
}

// gallery returns the gallery a paragraph holds: [gallery] on its first
// line and one or more images, with nothing else, under it. Otherwise nil.
func gallery(p *ast.Paragraph, src []byte) *galleryNode {
	lines := p.Lines()
	if lines.Len() < 2 {
		return nil
	}
	first := lines.At(0)
	if strings.TrimSpace(string(first.Value(src))) != galleryMark {
		return nil
	}
	firstEnd := first.Stop
	g := &galleryNode{}
	var mark strings.Builder
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Image:
			s, _ := splitAlign(string(v.Destination))
			g.items = append(g.items, galleryItem{src: s, alt: nodeText(v, src), title: string(v.Title)})
		case *ast.Text:
			// The mark's own text, or the line breaks between images.
			if v.Segment.Start < firstEnd {
				mark.Write(v.Segment.Value(src))
			} else if !blankText(v, src) {
				return nil
			}
		default:
			return nil
		}
	}
	if len(g.items) == 0 || strings.TrimSpace(mark.String()) != galleryMark {
		return nil
	}
	return g
}

// blockTransformer turns image-only and YouTube-only paragraphs into
// figures and embeds.
type blockTransformer struct{}

func (blockTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	var paras []*ast.Paragraph
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if p, ok := n.(*ast.Paragraph); ok && entering {
			paras = append(paras, p)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, p := range paras {
		if g := gallery(p, src); g != nil {
			if parent := p.Parent(); parent != nil {
				parent.ReplaceChild(parent, p, g)
			}
			continue
		}
		var images []*ast.Image
		var link string
		only := true
		count := 0
		for c := p.FirstChild(); c != nil; c = c.NextSibling() {
			if blankText(c, src) {
				continue
			}
			count++
			switch v := c.(type) {
			case *ast.Image:
				images = append(images, v)
			case *ast.AutoLink:
				// Only a bare URL embeds; [text](url) stays a link.
				link = string(v.URL(src))
			default:
				only = false
			}
		}
		if !only || count == 0 {
			continue
		}
		parent := p.Parent()
		if parent == nil {
			continue
		}
		if len(images) == count {
			for _, im := range images {
				s, align := splitAlign(string(im.Destination))
				fig := &figureNode{src: s, alt: nodeText(im, src), caption: string(im.Title), align: align}
				parent.InsertBefore(parent, p, fig)
			}
			parent.RemoveChild(parent, p)
			continue
		}
		if count == 1 && link != "" {
			if id := youtubeVideo(link); id != "" {
				parent.ReplaceChild(parent, p, &embedNode{video: id})
			} else if player, song := appleMusic(link); player != "" {
				parent.ReplaceChild(parent, p, &embedNode{music: player, song: song})
			}
		}
	}
}

// headingIDs gives headings anchors from their text, unique per document.
type headingIDs struct{ seen map[string]bool }

func (h *headingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	base := headingID(string(value))
	id := base
	for i := 1; h.seen[id]; i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	h.seen[id] = true
	return []byte(id)
}

func (h *headingIDs) Put(value []byte) { h.seen[string(value)] = true }

// imageRenderer writes figures, embeds and inline images. Images uploaded
// to the blog get width, height and a srcset of their resized copies.
type imageRenderer struct {
	lookup func(key string) (Image, bool)
}

func (r *imageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindFigure, r.renderFigure)
	reg.Register(kindEmbed, r.renderEmbed)
	reg.Register(kindGallery, r.renderGallery)
	reg.Register(ast.KindImage, r.renderImage)
}

// sizes per placement: the text column is 720 px wide; floats take about
// 45% of it, and phones show every image across the screen.
var imageSizes = map[string]string{
	"center": "(min-width: 760px) 720px, calc(100vw - 32px)",
	"left":   "(min-width: 760px) 330px, (min-width: 560px) 45vw, calc(100vw - 32px)",
	"right":  "(min-width: 760px) 330px, (min-width: 560px) 45vw, calc(100vw - 32px)",
}

// srcset lists an uploaded image's copies for the srcset attribute.
func srcset(im Image) string {
	var set []string
	for _, wd := range im.Widths {
		set = append(set, fmt.Sprintf("%s %dw", variantURL(im.Key, im.Width, wd), wd))
	}
	return strings.Join(set, ", ")
}

// writeImg writes an image. An uploaded one, whose size is known, is
// wrapped in a link to its full copy that the gallery on post pages opens
// (static/gallery.js); zoom is false where the image is already a link.
func (r *imageRenderer) writeImg(w util.BufWriter, src, alt, title, sizes string, zoom bool) {
	esc := html.EscapeString
	var im Image
	known := false
	if key, ok := strings.CutPrefix(src, "/img/"); ok && r.lookup != nil {
		im, known = r.lookup(key)
	}
	if known && zoom {
		fmt.Fprintf(w, `<a class="zoom" href="%s" data-pswp-width="%d" data-pswp-height="%d"`, esc(src), im.Width, im.Height)
		if len(im.Widths) > 1 {
			fmt.Fprintf(w, ` data-pswp-srcset="%s"`, esc(srcset(im)))
		}
		w.WriteString(">")
	}
	fmt.Fprintf(w, `<img src="%s" alt="%s"`, esc(src), esc(alt))
	if title != "" {
		fmt.Fprintf(w, ` title="%s"`, esc(title))
	}
	if known {
		fmt.Fprintf(w, ` width="%d" height="%d"`, im.Width, im.Height)
		if len(im.Widths) > 1 {
			fmt.Fprintf(w, ` srcset="%s" sizes="%s"`, esc(srcset(im)), sizes)
		}
	}
	w.WriteString(` loading="lazy" decoding="async">`)
	if known && zoom {
		w.WriteString("</a>")
	}
}

// inLink reports whether n sits inside a link.
func inLink(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == ast.KindLink || p.Kind() == ast.KindAutoLink {
			return true
		}
	}
	return false
}

func (r *imageRenderer) renderFigure(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*figureNode)
	fmt.Fprintf(w, "<figure class=\"fig fig-%s\">", n.align)
	r.writeImg(w, n.src, n.alt, "", imageSizes[n.align], true)
	if n.caption != "" {
		fmt.Fprintf(w, "<figcaption>%s</figcaption>", html.EscapeString(n.caption))
	}
	w.WriteString("</figure>\n")
	return ast.WalkSkipChildren, nil
}

// Gallery tiles: the class gr-N gives a tile the proportions N/20 (width
// over height) and a share N of its row's width, so that the tiles in a
// row come out the same height (content.css has a rule per N; the CSP
// takes no style attributes). N runs from 5 to 80, a picture 4 times
// taller than wide to one 4 times wider; an image of unknown size gets 3:2.
const (
	galleryRatioMin     = 5
	galleryRatioMax     = 80
	galleryRatioUnknown = 30
	galleryWidth        = 720 // px, the text column
	galleryGap          = 6   // px, as in content.css
	galleryRowHeight    = 180 // px, the height rows aim at
)

func galleryRatio(im Image, known bool) int {
	if !known || im.Width <= 0 || im.Height <= 0 {
		return galleryRatioUnknown
	}
	n := int(math.Round(float64(im.Width) * 20 / float64(im.Height)))
	return min(max(n, galleryRatioMin), galleryRatioMax)
}

// galleryRows splits tiles of proportions n[i]/20 into rows, in order, so
// that the rows' heights at the column's width come as close as they can
// to galleryRowHeight (least squares over all rows). The last row may stay
// short of the width rather than grow taller than that, at a cost for the
// empty room. It returns the
// index where each row starts.
func galleryRows(n []int) []int {
	height := func(i, j int) float64 { // tiles i..j-1 across the column
		sum := 0
		for _, v := range n[i:j] {
			sum += v
		}
		return float64(galleryWidth-galleryGap*(j-i-1)) * 20 / float64(sum)
	}
	cost := make([]float64, len(n)+1)
	from := make([]int, len(n)+1)
	for j := 1; j <= len(n); j++ {
		cost[j] = math.Inf(1)
		for i := 0; i < j; i++ {
			h := height(i, j)
			d := h - galleryRowHeight
			if j == len(n) && d > 0 {
				// A last row kept at the aim's height leaves part of the
				// width empty; that counts as much as the same share of
				// height missing.
				d = galleryRowHeight * (1 - galleryRowHeight/h)
			}
			if c := cost[i] + d*d; c < cost[j] {
				cost[j], from[j] = c, i
			}
		}
	}
	var starts []int
	for j := len(n); j > 0; j = from[j] {
		starts = append([]int{from[j]}, starts...)
	}
	return starts
}

func (r *imageRenderer) renderGallery(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*galleryNode)
	ratios := make([]int, len(n.items))
	for i, it := range n.items {
		var im Image
		known := false
		if key, ok := strings.CutPrefix(it.src, "/img/"); ok && r.lookup != nil {
			im, known = r.lookup(key)
		}
		ratios[i] = galleryRatio(im, known)
	}
	starts := galleryRows(ratios)
	w.WriteString(`<div class="gallery">`)
	for k, i := range starts {
		j := len(ratios)
		if k+1 < len(starts) {
			j = starts[k+1]
		}
		sum := 0
		for _, v := range ratios[i:j] {
			sum += v
		}
		// A last row that would be taller than the aim keeps that height:
		// an empty tile takes the rest of the width.
		fill := 0
		if j == len(ratios) {
			full := int(math.Round(float64(galleryWidth-galleryGap*(j-i)) * 20 / galleryRowHeight))
			if full > sum {
				fill = full - sum
			}
		}
		w.WriteString(`<div class="grow">`)
		for x := i; x < j; x++ {
			it := n.items[x]
			// The tile's width, for srcset: its share of the column on a
			// wide screen; on a phone (content.css) N * 5.5 px, grown by up
			// to about half to fill its line, out of about 375.
			px := int(math.Ceil(float64(ratios[x]) / float64(sum+fill) * galleryWidth))
			vw := min(100, int(math.Ceil(float64(ratios[x])*2.2)))
			fmt.Fprintf(w, `<div class="gi gr-%d">`, ratios[x])
			r.writeImg(w, it.src, it.alt, it.title, fmt.Sprintf("(min-width: 760px) %dpx, %dvw", px, vw), true)
			w.WriteString("</div>")
		}
		if fill > 0 {
			fmt.Fprintf(w, `<span class="gfill gr-%d"></span>`, min(fill, galleryRatioMax))
		}
		w.WriteString("</div>")
	}
	w.WriteString("</div>\n")
	return ast.WalkSkipChildren, nil
}

func (r *imageRenderer) renderEmbed(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*embedNode)
	if n.music != "" {
		// Apple's own embed code, with the size set in content.css: the
		// site's CSP takes no style attributes.
		kind := "album"
		if n.song {
			kind = "song"
		}
		fmt.Fprintf(w, `<div class="embed embed-music embed-music-%s"><iframe src="%s" title="Apple Music" allow="autoplay *; encrypted-media *; clipboard-write" sandbox="allow-forms allow-popups allow-same-origin allow-scripts allow-storage-access-by-user-activation allow-top-navigation-by-user-activation" loading="lazy"></iframe></div>`+"\n", kind, html.EscapeString(n.music))
		return ast.WalkSkipChildren, nil
	}
	id := n.video
	fmt.Fprintf(w, `<div class="embed"><iframe src="https://www.youtube-nocookie.com/embed/%s" title="YouTube video" allow="accelerometer; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" referrerpolicy="strict-origin-when-cross-origin" allowfullscreen></iframe></div>`+"\n", id)
	return ast.WalkSkipChildren, nil
}

func (r *imageRenderer) renderImage(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	s, _ := splitAlign(string(n.Destination))
	r.writeImg(w, s, nodeText(n, src), string(n.Title), imageSizes["center"], !inLink(n))
	return ast.WalkSkipChildren, nil
}

type markdown struct {
	md goldmark.Markdown
}

// externalURL reports whether u is a web address on another site than
// host. Relative links, anchors and mail links stay on the blog.
func externalURL(u, host string) bool {
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return false
	}
	return host == "" || !strings.EqualFold(pu.Hostname(), host)
}

// linkTargets opens links to other sites in a new tab, so following one
// does not take the reader away from the post.
type linkTargets struct{ host string }

func (t linkTargets) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest string
		switch v := n.(type) {
		case *ast.Link:
			dest = string(v.Destination)
		case *ast.AutoLink:
			if v.AutoLinkType == ast.AutoLinkURL {
				dest = string(v.URL(src))
			}
		}
		if dest != "" && externalURL(dest, t.host) {
			n.SetAttributeString("target", []byte("_blank"))
			n.SetAttributeString("rel", []byte("noopener"))
		}
		return ast.WalkContinue, nil
	})
}

// newMarkdown makes the renderer; host is the blog's own, whose absolute
// links stay in the tab.
func newMarkdown(lookup func(key string) (Image, bool), host string) *markdown {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote, dialectExtras{}),
		goldmark.WithParserOptions(
			parser.WithAttribute(),
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(blockTransformer{}, 100), util.Prioritized(linkTargets{host: host}, 300)),
		),
		goldmark.WithRendererOptions(
			gmhtml.WithUnsafe(),
			renderer.WithNodeRenderers(util.Prioritized(&imageRenderer{lookup: lookup}, 100)),
		),
	)
	return &markdown{md: md}
}

// render converts Markdown to HTML. It must not be called under the store
// lock: image lookups take it.
func (m *markdown) render(body string) (string, error) {
	var buf bytes.Buffer
	ctx := parser.NewContext(parser.WithIDs(&headingIDs{seen: map[string]bool{}}))
	if err := m.md.Convert([]byte(body), &buf, parser.WithContext(ctx)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

var mediaRe = regexp.MustCompile(`(?s)<figure\b.*?</figure>|<div class="embed[^"]*">.*?</div>|<table\b.*?</table>|<nav class="toc">.*?</nav>|<div class="footnotes".*?</div>`)

// noteRefRe finds the footnote marks in the text, which go without a space.
var noteRefRe = regexp.MustCompile(`(?s)<sup id="fnref:.*?</sup>`)

// plainText strips tags from rendered HTML. Figures (with their captions),
// players, tables, the table of contents and footnotes are left out: they
// do not read as text.
func plainText(h string) string {
	h = mediaRe.ReplaceAllString(noteRefRe.ReplaceAllString(h, ""), " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(h, " ")), " "))
}

// excerpt shortens text to about n characters at a word boundary.
func excerpt(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexAny(cut, "  "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:—–-") + "…"
}
