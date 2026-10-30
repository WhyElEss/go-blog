#!/usr/bin/env python3
"""Convert a Ghost JSON export into the blog's import file.

    python3 tools/ghost2md.py ghost-export.json > import.json

Reads the export written by Ghost's Settings → Labs → Export (a "db" list
whose "data" has posts, tags, posts_tags, settings and, optionally,
custom_theme_settings for the dark-mode logo), turns
each post's HTML into Markdown and keeps image paths as /content/images/…;
`blog import` then copies the images from Ghost's content folder.

Only standard library. Handles what Ghost's editor produces: paragraphs,
headings with anchors, lists, quotes, links, bold/italic, image cards with
captions, YouTube embeds and HTML tables.
"""
import json
import re
import sys
import urllib.parse
from html.parser import HTMLParser

GHOST_URL = "__GHOST_URL__"


class Node:
    def __init__(self, tag, attrs=None, parent=None):
        self.tag = tag
        self.attrs = dict(attrs or [])
        self.children = []
        self.parent = parent

    def cls(self):
        return self.attrs.get("class", "").split()


VOID = {"br", "img", "hr", "input", "meta", "link", "source", "wbr"}


class Tree(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node("root")
        self.cur = self.root

    def handle_starttag(self, tag, attrs):
        n = Node(tag, attrs, self.cur)
        self.cur.children.append(n)
        if tag not in VOID:
            self.cur = n

    def handle_startendtag(self, tag, attrs):
        self.cur.children.append(Node(tag, attrs, self.cur))

    def handle_endtag(self, tag):
        n = self.cur
        while n is not self.root and n.tag != tag:
            n = n.parent
        if n is not self.root:
            self.cur = n.parent

    def handle_data(self, data):
        self.cur.children.append(data)


def local(url):
    """Ghost's own URLs become root-relative paths."""
    url = (url or "").replace(GHOST_URL, "")
    return url


def esc(text):
    text = re.sub(r"([\\`*_\[\]])", r"\\\1", text)
    return text


def inline(node):
    out = []
    for c in node.children:
        if isinstance(c, str):
            out.append(esc(re.sub(r"\s+", " ", c)))
            continue
        t = c.tag
        if t in ("strong", "b"):
            s = inline(c).strip()
            out.append(f"**{s}**" if s else "")
        elif t in ("em", "i"):
            s = inline(c).strip()
            out.append(f"_{s}_" if s else "")
        elif t == "code":
            out.append("`" + text_of(c) + "`")
        elif t == "a":
            href = local(c.attrs.get("href", ""))
            if href.startswith("#"):
                href = "#" + urllib.parse.unquote(href[1:])
            out.append(f"[{inline(c).strip()}]({href})")
        elif t == "br":
            out.append("  \n")
        elif t == "img":
            out.append(f"![{esc(c.attrs.get('alt', ''))}]({local(c.attrs.get('src'))})")
        else:
            out.append(inline(c))
    return "".join(out)


def text_of(node):
    if isinstance(node, str):
        return node
    return "".join(text_of(c) for c in node.children)


def clean_lines(s):
    lines = [ln.strip() for ln in s.split("\n")]
    return "\n".join(lines).strip()


def youtube(src):
    m = re.search(r"youtube(?:-nocookie)?\.com/embed/([A-Za-z0-9_-]{11})", src or "")
    return f"https://www.youtube.com/watch?v={m.group(1)}" if m else None


def table(node):
    rows = []
    for tr in walk(node, "tr"):
        cells = [clean_lines(inline(td)).replace("|", "\\|").replace("\n", " ") for td in tr.children
                 if not isinstance(td, str) and td.tag in ("td", "th")]
        rows.append(cells)
    if not rows:
        return ""
    width = max(len(r) for r in rows)
    rows = [r + [""] * (width - len(r)) for r in rows]
    out = ["| " + " | ".join(rows[0]) + " |", "|" + "---|" * width]
    out += ["| " + " | ".join(r) + " |" for r in rows[1:]]
    return "\n".join(out)


def walk(node, tag):
    for c in node.children:
        if isinstance(c, str):
            continue
        if c.tag == tag:
            yield c
        yield from walk(c, tag)


def list_block(node, depth=0):
    out = []
    n = 1
    for li in node.children:
        if isinstance(li, str):
            continue
        if li.tag in ("ul", "ol"):
            # Ghost sometimes nests a list straight in a list (<ul><ul>):
            # keep its items at this level.
            out.append(list_block(li, depth))
            continue
        if li.tag != "li":
            continue
        marker = f"{n}." if node.tag == "ol" else "-"
        n += 1
        text_parts, sub = [], []
        for c in li.children:
            if not isinstance(c, str) and c.tag in ("ul", "ol"):
                sub.append(list_block(c, depth + 1))
            elif not isinstance(c, str) and c.tag == "p":
                text_parts.append(inline(c))
            else:
                holder = Node("span")
                holder.children = [c]
                text_parts.append(inline(holder))
        indent = "   " * depth if node.tag == "ol" else "  " * depth
        line = clean_lines(" ".join(text_parts)).replace("\n", "\n" + indent + "  ")
        out.append(f"{indent}{marker} {line}".rstrip())
        out += sub
    return "\n".join(out)


def blocks(node):
    out = []
    for c in node.children:
        if isinstance(c, str):
            if c.strip():
                out.append(esc(c.strip()))
            continue
        t = c.tag
        if t == "p":
            s = clean_lines(inline(c))
            if s:
                out.append(s)
        elif re.fullmatch(r"h[1-6]", t):
            level = max(2, int(t[1]))
            hid = urllib.parse.unquote(c.attrs.get("id", ""))
            s = clean_lines(inline(c)).replace("\n", " ")
            out.append("#" * level + " " + s + (f" {{#{hid}}}" if hid else ""))
        elif t in ("ul", "ol"):
            out.append(list_block(c))
        elif t == "blockquote":
            inner = blocks(c) if any(not isinstance(x, str) and x.tag == "p" for x in c.children) else clean_lines(inline(c))
            out.append("\n".join("> " + ln if ln else ">" for ln in inner.split("\n")))
        elif t == "hr":
            out.append("---")
        elif t == "figure":
            iframe = next(walk(c, "iframe"), None)
            img = next(walk(c, "img"), None)
            if iframe is not None:
                url = youtube(iframe.attrs.get("src"))
                out.append(url or "")
            elif img is not None:
                cap = next(walk(c, "figcaption"), None)
                caption = clean_lines(text_of(cap)).replace('"', "'") if cap is not None else ""
                alt = esc(img.attrs.get("alt", ""))
                title = f' "{caption}"' if caption else ""
                out.append(f"![{alt}]({local(img.attrs.get('src'))}{title})")
        elif t == "table":
            out.append(table(c))
        elif t == "iframe":
            url = youtube(c.attrs.get("src"))
            if url:
                out.append(url)
        elif t in ("div", "section", "span"):
            out.append(blocks(c))
        else:
            s = clean_lines(inline(c))
            if s:
                out.append(s)
    return "\n\n".join(x for x in out if x)


def loose(anchor):
    return re.sub(r"[^\w-]", "", anchor.lower())


def html_to_md(html):
    tree = Tree()
    tree.feed(re.sub(r"<!--.*?-->", "", html or "", flags=re.S))
    md = blocks(tree.root).strip() + "\n"
    # Ghost's own table of contents sometimes links "#lets-talk" to a
    # heading whose id is "let’s-talk": give such headings the id the
    # links use.
    targets = {loose(h): h for h in re.findall(r"\]\(#([^)\s]+)\)", md)}
    def fix(m):
        hid = m.group(1)
        return "{#" + targets.get(loose(hid), hid) + "}"
    return re.sub(r"\{#([^}]+)\}(?=\n|$)", fix, md)


def iso(ts):
    if not ts:
        return None
    ts = str(ts).replace(" ", "T")
    if ts.endswith("Z") or "+" in ts[10:]:
        return ts
    return ts.split(".")[0] + "Z"  # Ghost stores UTC


def main():
    raw = json.load(open(sys.argv[1], encoding="utf-8"))
    data = raw["db"][0]["data"] if "db" in raw else raw
    tags = {t["id"]: t["name"] for t in data.get("tags", [])}
    post_tags = {}
    for pt in sorted(data.get("posts_tags", []), key=lambda x: x.get("sort_order", 0)):
        post_tags.setdefault(pt["post_id"], []).append(tags.get(pt["tag_id"]))
    settings = {s["key"]: s["value"] for s in data.get("settings", [])}
    theme = {t["key"]: t["value"] for t in data.get("custom_theme_settings", [])
             if t.get("theme") == settings.get("active_theme")}

    posts = []
    for p in data["posts"]:
        if p.get("status") != "published":
            continue
        title = p["title"]
        lang = "ru" if re.search("[а-яА-ЯёЁ]", title) else "en"
        posts.append({
            "slug": p["slug"],
            "title": title,
            "body": html_to_md(p.get("html")),
            "excerpt": p.get("custom_excerpt") or "",
            "cover": local(p.get("feature_image")),
            "tags": [t for t in post_tags.get(p["id"], []) if t],
            "page": p.get("type") == "page",
            "lang": lang,
            "published_at": iso(p.get("published_at")),
            "created_at": iso(p.get("created_at")),
            "updated_at": iso(p.get("updated_at")),
        })

    def links(v):
        try:
            return [{"label": x["label"], "url": x["url"]} for x in json.loads(v or "[]")]
        except ValueError:
            return []

    out = {
        "settings": {
            "title": settings.get("title", "Blog"),
            "description": settings.get("description", ""),
            "lang": "ru" if settings.get("locale", "en").startswith("ru") else "en",
            "nav": links(settings.get("navigation")),
            "footer_nav": links(settings.get("secondary_navigation")),
            "logo": local(settings.get("logo")),
            "logo_dark": local(theme.get("white_logo_for_dark_mode")),
            "icon": local(settings.get("icon")),
            "posts_per_page": 10,
        },
        "posts": posts,
    }
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)


if __name__ == "__main__":
    main()
