package main

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"regexp"
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
//   - A YouTube link alone in its paragraph becomes an embedded player.
//   - Headings take explicit anchors: ## Title {#anchor}. Others get one
//     made from their text.
//   - Raw HTML passes through: only the admin writes posts.

var (
	kindFigure = ast.NewNodeKind("Figure")
	kindEmbed  = ast.NewNodeKind("Embed")
)

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
}

func (n *embedNode) Kind() ast.NodeKind { return kindEmbed }
func (n *embedNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"video": n.video}, nil)
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
	reg.Register(ast.KindImage, r.renderImage)
}

// sizes per placement: the text column is 720 px wide; floats take about
// 45% of it, and phones show every image across the screen.
var imageSizes = map[string]string{
	"center": "(min-width: 760px) 720px, calc(100vw - 32px)",
	"left":   "(min-width: 760px) 330px, (min-width: 560px) 45vw, calc(100vw - 32px)",
	"right":  "(min-width: 760px) 330px, (min-width: 560px) 45vw, calc(100vw - 32px)",
}

func (r *imageRenderer) writeImg(w util.BufWriter, src, alt, title, align string) {
	esc := html.EscapeString
	fmt.Fprintf(w, `<img src="%s" alt="%s"`, esc(src), esc(alt))
	if title != "" {
		fmt.Fprintf(w, ` title="%s"`, esc(title))
	}
	if key, ok := strings.CutPrefix(src, "/img/"); ok && r.lookup != nil {
		if im, ok := r.lookup(key); ok {
			fmt.Fprintf(w, ` width="%d" height="%d"`, im.Width, im.Height)
			if len(im.Widths) > 1 {
				var set []string
				for _, wd := range im.Widths {
					set = append(set, fmt.Sprintf("%s %dw", esc(variantURL(im.Key, im.Width, wd)), wd))
				}
				fmt.Fprintf(w, ` srcset="%s" sizes="%s"`, strings.Join(set, ", "), imageSizes[align])
			}
		}
	}
	w.WriteString(` loading="lazy" decoding="async">`)
}

func (r *imageRenderer) renderFigure(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*figureNode)
	fmt.Fprintf(w, "<figure class=\"fig fig-%s\">", n.align)
	r.writeImg(w, n.src, n.alt, "", n.align)
	if n.caption != "" {
		fmt.Fprintf(w, "<figcaption>%s</figcaption>", html.EscapeString(n.caption))
	}
	w.WriteString("</figure>\n")
	return ast.WalkSkipChildren, nil
}

func (r *imageRenderer) renderEmbed(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	id := node.(*embedNode).video
	fmt.Fprintf(w, `<div class="embed"><iframe src="https://www.youtube-nocookie.com/embed/%s" title="YouTube video" allow="accelerometer; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" referrerpolicy="strict-origin-when-cross-origin" allowfullscreen></iframe></div>`+"\n", id)
	return ast.WalkSkipChildren, nil
}

func (r *imageRenderer) renderImage(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	s, _ := splitAlign(string(n.Destination))
	r.writeImg(w, s, nodeText(n, src), string(n.Title), "center")
	return ast.WalkSkipChildren, nil
}

type markdown struct {
	md goldmark.Markdown
}

func newMarkdown(lookup func(key string) (Image, bool)) *markdown {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAttribute(),
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(blockTransformer{}, 100)),
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

var mediaRe = regexp.MustCompile(`(?s)<figure\b.*?</figure>|<div class="embed">.*?</div>|<table\b.*?</table>`)

// plainText strips tags from rendered HTML. Figures (with their captions),
// players and tables are left out: they do not read as text.
func plainText(h string) string {
	h = mediaRe.ReplaceAllString(h, " ")
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
