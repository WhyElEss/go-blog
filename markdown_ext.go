package main

import (
	"html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Two additions to the dialect, written the way goldmark's own extensions
// are:
//
//   - ==text== highlights, as <mark>. A single = stays text.
//   - [TOC] alone in its paragraph becomes a table of contents of the
//     H2–H4 headings that follow it, linked to their anchors.

var (
	kindMark = ast.NewNodeKind("Mark")
	kindTOC  = ast.NewNodeKind("TOC")
)

type markNode struct{ ast.BaseInline }

func (n *markNode) Kind() ast.NodeKind { return kindMark }
func (n *markNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}

type markDelimiter struct{}

func (markDelimiter) IsDelimiter(b byte) bool { return b == '=' }
func (markDelimiter) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}
func (markDelimiter) OnMatch(consumes int) ast.Node { return &markNode{} }

type markParser struct{}

func (markParser) Trigger() []byte { return []byte{'='} }

func (markParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	node := parser.ScanDelimiter(line, before, 2, markDelimiter{})
	if node == nil || node.OriginalLength != 2 || before == '=' {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

func (markParser) CloseBlock(parent ast.Node, pc parser.Context) {}

type tocItem struct {
	level    int
	id, text string
}

type tocNode struct {
	ast.BaseBlock
	items []tocItem
}

func (n *tocNode) Kind() ast.NodeKind { return kindTOC }
func (n *tocNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, nil, nil)
}

// tocTransformer runs after the headings have their anchors.
type tocTransformer struct{}

func (tocTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	var marks []ast.Node
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if p, ok := c.(*ast.Paragraph); ok && strings.TrimSpace(nodeText(p, src)) == "[TOC]" {
			marks = append(marks, p)
		}
	}
	if len(marks) == 0 {
		return
	}
	for _, m := range marks {
		toc := &tocNode{}
		for c := m.NextSibling(); c != nil; c = c.NextSibling() {
			h, ok := c.(*ast.Heading)
			if !ok || h.Level < 2 || h.Level > 4 {
				continue
			}
			id, _ := h.AttributeString("id")
			idb, _ := id.([]byte)
			if len(idb) == 0 {
				continue
			}
			toc.items = append(toc.items, tocItem{level: h.Level, id: string(idb), text: nodeText(h, src)})
		}
		doc.ReplaceChild(doc, m, toc)
	}
}

type extRenderer struct{}

func (extRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMark, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			w.WriteString("<mark>")
		} else {
			w.WriteString("</mark>")
		}
		return ast.WalkContinue, nil
	})
	reg.Register(kindTOC, renderTOC)
}

// renderTOC writes nested lists: a deeper heading opens a list inside the
// item before it, a shallower one closes lists back to its level.
func renderTOC(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	items := node.(*tocNode).items
	if len(items) == 0 {
		return ast.WalkContinue, nil
	}
	w.WriteString(`<nav class="toc">` + "\n<ul>\n")
	var levels []int // the level of each open list
	levels = append(levels, items[0].level)
	for i, it := range items {
		if i > 0 {
			switch top := levels[len(levels)-1]; {
			case it.level > top:
				w.WriteString("\n<ul>\n")
				levels = append(levels, it.level)
			default:
				w.WriteString("</li>\n")
				for len(levels) > 1 && it.level <= levels[len(levels)-2] {
					w.WriteString("</ul>\n</li>\n")
					levels = levels[:len(levels)-1]
				}
				// Between two open levels (H2, H4, then H3): it joins
				// the inner list.
				if it.level < levels[len(levels)-1] {
					levels[len(levels)-1] = it.level
				}
			}
		}
		w.WriteString(`<li><a href="#` + html.EscapeString(it.id) + `">` + html.EscapeString(it.text) + "</a>")
	}
	w.WriteString("</li>\n")
	for len(levels) > 1 {
		w.WriteString("</ul>\n</li>\n")
		levels = levels[:len(levels)-1]
	}
	w.WriteString("</ul>\n</nav>\n")
	return ast.WalkContinue, nil
}

type dialectExtras struct{}

func (dialectExtras) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithInlineParsers(util.Prioritized(markParser{}, 500)),
		parser.WithASTTransformers(util.Prioritized(tocTransformer{}, 200)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(extRenderer{}, 500)))
}
