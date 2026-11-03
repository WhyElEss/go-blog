package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	a, err := newApp(Config{DataDir: t.TempDir(), AdminHash: h, BaseURL: "https://blog.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func adminCookieFor(a *App) *http.Cookie {
	return &http.Cookie{Name: adminCookie, Value: a.signSession(time.Now().Add(time.Hour))}
}

func do(t *testing.T, a *App, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.routes().ServeHTTP(rec, req)
	return rec
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello, World!":          "hello-world",
		"Возрождение винила":     "vozrozhdenie-vinila",
		"  Dirty   Loops  ":      "dirty-loops",
		"Café № 5":               "caf-5",
		"Щука и ёж":              "shchuka-i-ezh",
		"Їжак і ґанок":           "yizhak-i-ganok",
		"---":                    "",
		"Vinyl’s Modern Revival": "vinyl-s-modern-revival",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownFigures(t *testing.T) {
	md := newMarkdown(func(key string) (Image, bool) {
		if key == "2026/10/a.jpg" {
			return Image{Key: key, Width: 2000, Height: 1000, Widths: []int{600, 1000, 1400, 2000}}, true
		}
		return Image{}, false
	}, "")
	h, err := md.render("![Turntable](/img/2026/10/a.jpg#right \"A caption\")\n\nText beside it.\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<figure class="fig fig-right">`,
		`src="/img/2026/10/a.jpg"`,
		`alt="Turntable"`,
		`width="2000" height="1000"`,
		`/img/2026/10/a-w600.jpg 600w`,
		`/img/2026/10/a.jpg 2000w`,
		`<figcaption>A caption</figcaption>`,
		`<p>Text beside it.</p>`,
		`<a class="zoom" href="/img/2026/10/a.jpg" data-pswp-width="2000" data-pswp-height="1000" data-pswp-srcset="/img/2026/10/a-w600.jpg 600w, `,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
	if strings.Contains(h, "<p><figure") {
		t.Errorf("figure inside a paragraph:\n%s", h)
	}

	if got := plainText(h); got != "Text beside it." {
		t.Errorf("plainText = %q, want the caption left out", got)
	}

	h, _ = md.render("![](/img/x.png)\n![](/img/y.png#left)\n")
	if strings.Count(h, "<figure") != 2 || !strings.Contains(h, "fig-center") || !strings.Contains(h, "fig-left") {
		t.Errorf("two images in one paragraph:\n%s", h)
	}

	// An image that is already a link, or not an upload, opens no gallery.
	h, _ = md.render("[![a](/img/2026/10/a.jpg)](https://example.org/) and ![x](/img/x.png)\n")
	if strings.Contains(h, "zoom") {
		t.Errorf("zoom link where there should be none:\n%s", h)
	}

	h, _ = md.render("Inline ![icon](/img/i.png) in text.\n")
	if strings.Contains(h, "<figure") || !strings.Contains(h, `<img src="/img/i.png" alt="icon"`) {
		t.Errorf("inline image:\n%s", h)
	}
}

func TestMarkdownEmbedsAndHeadings(t *testing.T) {
	md := newMarkdown(nil, "blog.example.com")
	h, _ := md.render("https://www.youtube.com/watch?v=gYc5zrXC52k\n\n[a link](https://youtu.be/gYc5zrXC52k)\n")
	if !strings.Contains(h, `src="https://www.youtube-nocookie.com/embed/gYc5zrXC52k"`) {
		t.Errorf("no embed:\n%s", h)
	}
	if strings.Count(h, "<iframe") != 1 {
		t.Errorf("a [text](url) link must stay a link:\n%s", h)
	}

	h, _ = md.render("https://music.apple.com/ca/album/im-a-rainbow-re-mastered-expanded/1587042364\n\nhttps://music.apple.com/ca/album/x/1587042364?i=1587042366&l=fr\n\nhttps://music.apple.com/ca/artist/x/123\n")
	for _, want := range []string{
		`<div class="embed embed-music embed-music-album"><iframe src="https://embed.music.apple.com/ca/album/im-a-rainbow-re-mastered-expanded/1587042364"`,
		`<div class="embed embed-music embed-music-song"><iframe src="https://embed.music.apple.com/ca/album/x/1587042364?i=1587042366"`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %s in\n%s", want, h)
		}
	}
	if strings.Count(h, "<iframe") != 2 || plainText(h) != "https://music.apple.com/ca/artist/x/123" {
		t.Errorf("an artist link must stay a link, players stay out of the text:\n%s", h)
	}

	h, _ = md.render("## Новый или ретро?\n\n## Belt Drive {#belt}\n\n## Belt Drive\n")
	for _, want := range []string{`<h2 id="новый-или-ретро">`, `<h2 id="belt">`, `<h2 id="belt-drive">`} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}

	h, _ = md.render("| A | B |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(h, "<table>") {
		t.Errorf("no table:\n%s", h)
	}
}

func TestMarkdownExtras(t *testing.T) {
	md := newMarkdown(nil, "blog.example.com")
	for in, want := range map[string]string{
		"a ==bright== word":     "<p>a <mark>bright</mark> word</p>",
		"x = 1 and y == 2":      "<p>x = 1 and y == 2</p>",
		"a ===three=== b":       "<p>a ===three=== b</p>",
		"`==code==`":            "<p><code>==code==</code></p>",
		"~~gone~~ and **bold**": "<p><del>gone</del> and <strong>bold</strong></p>",
	} {
		if h, _ := md.render(in); strings.TrimSpace(h) != want {
			t.Errorf("%q:\n got %s\nwant %s", in, strings.TrimSpace(h), want)
		}
	}

	// [TOC] lists the H2–H4 headings after it, nested by level, skipping
	// an H4 straight under an H2 into the inner list.
	h, _ := md.render("# Title\n\n[TOC]\n\n## One\n\n### One a {#oa}\n\n## Two\n\n#### Deep\n\n### Mid\n\n##### Too deep\n")
	want := `<nav class="toc">
<ul>
<li><a href="#one">One</a>
<ul>
<li><a href="#oa">One a</a></li>
</ul>
</li>
<li><a href="#two">Two</a>
<ul>
<li><a href="#deep">Deep</a></li>
<li><a href="#mid">Mid</a></li>
</ul>
</li>
</ul>
</nav>`
	if !strings.Contains(h, want) {
		t.Errorf("toc:\n%s", h)
	}
	if strings.Contains(h, "[TOC]") || strings.Contains(h, "Title</a>") || strings.Contains(h, "Too deep</a>") {
		t.Errorf("toc lists the wrong headings:\n%s", h)
	}
	// A [TOC] inside other text stays text.
	if h, _ := md.render("See [TOC] here.\n\n## A\n"); !strings.Contains(h, "See [TOC] here.") || strings.Contains(h, "toc") {
		t.Errorf("inline [TOC]:\n%s", h)
	}

	h, _ = md.render("Text[^1].\n\n[^1]: The note.\n")
	if !strings.Contains(h, `href="#fn:1"`) || !strings.Contains(h, `class="footnotes"`) || !strings.Contains(h, "The note.") {
		t.Errorf("footnote:\n%s", h)
	}
	if got := plainText(h); got != "Text." {
		t.Errorf("footnotes in the excerpt: %q", got)
	}
	if got := plainText(func() string { h, _ := md.render("[TOC]\n\n## A\n\nBody.\n"); return h }()); got != "A Body." {
		t.Errorf("toc in the excerpt: %q", got)
	}

	h, _ = md.render("- [ ] todo\n- [x] done\n")
	if strings.Count(h, `type="checkbox"`) != 2 {
		t.Errorf("task list:\n%s", h)
	}
}

func TestYoutubeVideo(t *testing.T) {
	ok := []string{
		"https://www.youtube.com/watch?v=5ZCjaHQJIm8",
		"https://youtu.be/5ZCjaHQJIm8",
		"https://www.youtube.com/embed/5ZCjaHQJIm8?feature=oembed",
		"https://m.youtube.com/watch?v=5ZCjaHQJIm8&t=10",
	}
	for _, u := range ok {
		if youtubeVideo(u) != "5ZCjaHQJIm8" {
			t.Errorf("youtubeVideo(%q) = %q", u, youtubeVideo(u))
		}
	}
	for _, u := range []string{"https://example.com/watch?v=5ZCjaHQJIm8", "https://youtube.com/channel/x", "javascript:alert(1)"} {
		if youtubeVideo(u) != "" {
			t.Errorf("youtubeVideo(%q) should be empty", u)
		}
	}
}

func encodeJPEG(t *testing.T, w, h int, exifOrientation int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/2 && y < h/2 {
				c = color.RGBA{255, 0, 0, 255} // top-left quarter red
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if exifOrientation == 0 {
		return b
	}
	// APP1 with a big-endian TIFF holding one IFD entry: Orientation.
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, byte(exifOrientation), 0, 0, 0, 0, 0, 0}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}, seg...)
	return append(append([]byte{0xFF, 0xD8}, app1...), b[2:]...)
}

func TestJPEGOrientation(t *testing.T) {
	for o := 1; o <= 8; o++ {
		if got := jpegOrientation(encodeJPEG(t, 8, 4, o)); got != o {
			t.Errorf("orientation %d read as %d", o, got)
		}
	}
	if got := jpegOrientation(encodeJPEG(t, 8, 4, 0)); got != 1 {
		t.Errorf("no EXIF read as %d", got)
	}
}

func TestProcessImage(t *testing.T) {
	dir := t.TempDir()
	none := func(string) bool { return false }
	when := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

	// 3000×1500 landscape: full copy 2000×1000 plus 600/1000/1400.
	im, err := processImage(dir, "My Photo.JPG", encodeJPEG(t, 3000, 1500, 0), when, none)
	if err != nil {
		t.Fatal(err)
	}
	if im.Key != "2026/10/my-photo.jpg" || im.Width != 2000 || im.Height != 1000 {
		t.Fatalf("got %+v", im)
	}
	if len(im.Widths) != 4 || im.Widths[3] != 2000 {
		t.Fatalf("widths %v", im.Widths)
	}
	for _, name := range []string{"my-photo.jpg", "my-photo-w600.jpg", "my-photo-w1000.jpg", "my-photo-w1400.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, "2026", "10", name)); err != nil {
			t.Error(err)
		}
	}

	// Phone photo stored sideways with orientation 6: 800×400 pixels shown
	// as 400×800, and the red corner ends up top-right.
	im, err = processImage(dir, "phone.jpg", encodeJPEG(t, 800, 400, 6), when, func(k string) bool { return k == "2026/10/phone.jpg" })
	if err != nil {
		t.Fatal(err)
	}
	if im.Key != "2026/10/phone-2.jpg" || im.Width != 400 || im.Height != 800 {
		t.Fatalf("rotated: %+v", im)
	}
	f, _ := os.Open(filepath.Join(dir, "2026", "10", "phone-2.jpg"))
	defer f.Close()
	got, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, b, _ := got.At(350, 50).RGBA(); r < b {
		t.Errorf("top-right should be red after rotation, got r=%d b=%d", r>>8, b>>8)
	}

	// Transparent PNG stays PNG; small images get no extra copies.
	png1 := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	var buf bytes.Buffer
	png.Encode(&buf, png1)
	im, err = processImage(dir, "logo.png", buf.Bytes(), when, none)
	if err != nil {
		t.Fatal(err)
	}
	if im.Key != "2026/10/logo.png" || len(im.Widths) != 1 {
		t.Fatalf("png: %+v", im)
	}

	im, err = processImage(dir, "diagram.svg", []byte(`<svg viewBox="0 0 1100 400" xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`), when, none)
	if err != nil {
		t.Fatal(err)
	}
	if im.Key != "2026/10/diagram.svg" || im.Width != 1100 || im.Height != 400 {
		t.Fatalf("svg: %+v", im)
	}
	if got := imageName("IMG_4310_x700.jpg.webp"); got != "img-4310-x700" {
		t.Errorf("imageName = %q", got)
	}

	if _, err := processImage(dir, "x.txt", []byte("hello"), when, none); err == nil {
		t.Error("text accepted as an image")
	}
}

func TestPublicPagesAndAdminFlow(t *testing.T) {
	a := testApp(t)
	ck := adminCookieFor(a)

	// Login.
	rec := do(t, a, httptest.NewRequest("GET", "/admin/", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin without login: %d", rec.Code)
	}
	form := url.Values{"password": {"wrong password"}}
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := do(t, a, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}

	// Create a draft, then publish it.
	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(ck)
		return do(t, a, req)
	}
	// Written only in the Russian tab: the post is in Russian.
	rec = post("/admin/posts", url.Values{"title_ru": {"Первая запись"}, "body_ru": {"Hello **world**.\n\n## Part {#part}\n"}, "tags": {"Vinyl, Music"}, "action": {"save"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/admin/posts/"), "?m=saved")
	if rec := do(t, a, httptest.NewRequest("GET", "/pervaya-zapis/", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("draft visible: %d", rec.Code)
	}
	rec = post("/admin/posts/"+id, url.Values{"title_ru": {"Первая запись"}, "slug": {"pervaya-zapis"}, "body_ru": {"Hello **world**.\n"}, "tags": {"Vinyl"}, "lang": {"ru"}, "action": {"publish"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}

	rec = do(t, a, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Первая запись") {
		t.Fatalf("index: %d", rec.Code)
	}
	rec = do(t, a, httptest.NewRequest("GET", "/pervaya-zapis/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<strong>world</strong>") || !strings.Contains(rec.Body.String(), `<html lang="ru">`) {
		t.Fatalf("post: %d\n%s", rec.Code, rec.Body)
	}
	if rec := do(t, a, httptest.NewRequest("GET", "/pervaya-zapis", nil)); rec.Code != http.StatusMovedPermanently {
		t.Fatalf("slashless: %d", rec.Code)
	}
	if rec := do(t, a, httptest.NewRequest("GET", "/tag/vinyl/", nil)); rec.Code != 200 {
		t.Fatalf("tag: %d", rec.Code)
	}
	if rec := do(t, a, httptest.NewRequest("GET", "/tag/nothing/", nil)); rec.Code != 404 {
		t.Fatalf("unknown tag: %d", rec.Code)
	}

	// Slug clash and reserved slugs are refused.
	rec = post("/admin/posts", url.Values{"title_en": {"Other"}, "slug": {"pervaya-zapis"}, "body_en": {"x"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("slug clash: %d", rec.Code)
	}
	rec = post("/admin/posts", url.Values{"title_en": {"Admin"}, "slug": {"admin"}, "body_en": {"x"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reserved slug: %d", rec.Code)
	}

	// A post scheduled for later stays hidden.
	future := time.Now().Add(48 * time.Hour).Format("2006-01-02T15:04")
	post("/admin/posts", url.Values{"title_en": {"Later"}, "body_en": {"soon"}, "published_at": {future}, "action": {"publish"}})
	if rec := do(t, a, httptest.NewRequest("GET", "/later/", nil)); rec.Code != 404 {
		t.Fatalf("scheduled visible: %d", rec.Code)
	}

	// RSS: one live post, absolute links.
	rec = do(t, a, httptest.NewRequest("GET", "/rss/", nil))
	body := rec.Body.String()
	if rec.Code != 200 || strings.Count(body, "<item>") != 1 || !strings.Contains(body, "<link>https://blog.example.com/pervaya-zapis/</link>") {
		t.Fatalf("rss: %d\n%s", rec.Code, body)
	}

	// Cross-site posts are refused.
	req = httptest.NewRequest("POST", "/admin/posts", strings.NewReader("title_en=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(ck)
	if rec := do(t, a, req); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site: %d", rec.Code)
	}
}

func TestUploadAndImageLifecycle(t *testing.T) {
	a := testApp(t)
	ck := adminCookieFor(a)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "Turntable.jpg")
	fw.Write(encodeJPEG(t, 1200, 800, 0))
	mw.Close()
	req := httptest.NewRequest("POST", "/admin/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(ck)
	rec := do(t, a, req)
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		URL           string
		Width, Height int
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !strings.HasPrefix(res.URL, "/img/") || !strings.HasSuffix(res.URL, "/turntable.jpg") || res.Width != 1200 {
		t.Fatalf("upload result %+v", res)
	}
	if rec := do(t, a, httptest.NewRequest("GET", res.URL, nil)); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("serve image: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := do(t, a, httptest.NewRequest("GET", "/img/../blog.json", nil)); rec.Code == 200 {
		t.Fatal("path traversal served a file")
	}

	// In use: delete is refused.
	key := strings.TrimPrefix(res.URL, "/img/")
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts, &Post{ID: "p1", Slug: "p", Title: "P", Body: "![](" + res.URL + "#left)"})
		return nil
	})
	form := url.Values{"key": {key}}
	req = httptest.NewRequest("POST", "/admin/images/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(ck)
	do(t, a, req)
	if _, ok := a.store.image(key); !ok {
		t.Fatal("an image in use was deleted")
	}
	if rec := do(t, a, httptest.NewRequest("GET", res.URL, nil)); rec.Code != 200 {
		t.Fatal("file of an image in use was removed")
	}
}

func TestBilingualPosts(t *testing.T) {
	a := testApp(t)
	ck := adminCookieFor(a)
	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(ck)
		return do(t, a, req)
	}
	get := func(path, acceptLang string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if acceptLang != "" {
			req.Header.Set("Accept-Language", acceptLang)
		}
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return do(t, a, req)
	}

	rec := post("/admin/posts", url.Values{
		"lang": {"en"}, "slug": {"turntables"}, "action": {"publish"},
		"title_en": {"Turntables"}, "body_en": {"English text."},
		"title_ru": {"Проигрыватели"}, "body_ru": {"Русский текст."}, "excerpt_ru": {"Анонс по-русски"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := post("/admin/posts", url.Values{"title_ru": {"Только по-русски"}, "body_ru": {"Текст."}, "action": {"publish"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("create ru: %d %s", rec.Code, rec.Body)
	}
	// A translation needs its own title.
	if rec := post("/admin/posts", url.Values{"title_en": {"X"}, "body_en": {"x"}, "body_ru": {"без заголовка"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("translation without title: %d", rec.Code)
	}

	ruCookie := &http.Cookie{Name: "lang", Value: "ru"}
	cases := []struct {
		name, path, accept string
		cookies            []*http.Cookie
		want, lang         string
	}{
		{"browser ru", "/turntables/", "ru-RU,ru;q=0.9,en;q=0.8", nil, "Русский текст.", "ru"},
		{"browser en", "/turntables/", "en-CA,en;q=0.9", nil, "English text.", "en"},
		{"neither: default", "/turntables/", "de-DE", nil, "English text.", "en"},
		{"cookie beats browser", "/turntables/", "en-US", []*http.Cookie{ruCookie}, "Русский текст.", "ru"},
		{"?lang beats cookie", "/turntables/?lang=en", "ru", []*http.Cookie{ruCookie}, "English text.", "en"},
	}
	for _, c := range cases {
		rec := get(c.path, c.accept, c.cookies...)
		b := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(b, c.want) || !strings.Contains(b, `<html lang="`+c.lang+`">`) {
			t.Errorf("%s: %d, want %q in %s\n%s", c.name, rec.Code, c.want, c.lang, b)
		}
		if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Language") || !strings.Contains(v, "Cookie") {
			t.Errorf("%s: Vary %q", c.name, v)
		}
	}
	b := get("/turntables/", "ru").Body.String()
	if !strings.Contains(b, `href="/turntables/?lang=en"`) || !strings.Contains(b, "Read in English") || !strings.Contains(b, "<title>Проигрыватели") || !strings.Contains(b, "Анонс по-русски") {
		t.Errorf("ru page: link to English, title or excerpt missing\n%s", b)
	}
	// No English version: the Russian text with a note, in English.
	b = get("/tolko-po-russki/", "en").Body.String()
	if !strings.Contains(b, "Текст.") || !strings.Contains(b, "only available in Russian") || !strings.Contains(b, `<html lang="ru">`) {
		t.Errorf("ru-only post for an English reader\n%s", b)
	}
	if b := get("/", "ru").Body.String(); !strings.Contains(b, "Проигрыватели") || strings.Contains(b, "Turntables") {
		t.Errorf("index for ru reader\n%s", b)
	}

	// The newest post is featured on the first page only; the rest are tiles.
	b = get("/", "en").Body.String()
	if strings.Count(b, `<article class="card"`) != 1 || strings.Count(b, `<article class="tile"`) != 1 || strings.Index(b, `class="card"`) > strings.Index(b, `class="tile"`) {
		t.Errorf("index layout\n%s", b)
	}
	a.store.update(func(d *Data) error { d.Settings.PostsPerPage = 1; return nil })
	b = get("/page/2/", "en").Body.String()
	if strings.Contains(b, `<article class="card"`) || strings.Count(b, `<article class="tile"`) != 1 {
		t.Errorf("page 2 layout\n%s", b)
	}
	a.store.update(func(d *Data) error { d.Settings.PostsPerPage = 10; return nil })

	// Feeds.
	b = get("/rss/", "ru").Body.String()
	if !strings.Contains(b, "<title>Turntables</title>") || !strings.Contains(b, "<link>https://blog.example.com/turntables/</link>") || !strings.Contains(b, "<language>en</language>") {
		t.Errorf("default feed\n%s", b)
	}
	b = get("/rss/ru/", "").Body.String()
	if !strings.Contains(b, "<title>Проигрыватели</title>") || !strings.Contains(b, "<link>https://blog.example.com/turntables/?lang=ru</link>") || !strings.Contains(b, "<language>ru</language>") || !strings.Contains(b, "Только по-русски") {
		t.Errorf("ru feed\n%s", b)
	}
	if rec := get("/rss/de/", ""); rec.Code != 404 {
		t.Errorf("unknown feed language: %d", rec.Code)
	}

	// The switch sets the cookie and goes back; foreign targets are refused.
	rec = get("/lang/ru?next=/turntables/", "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/turntables/" || !strings.Contains(rec.Header().Get("Set-Cookie"), "lang=ru") {
		t.Errorf("switch: %d %v", rec.Code, rec.Header())
	}
	for _, next := range []string{"//evil.example", "/\\evil.example", "https://evil.example"} {
		if loc := get("/lang/en?next="+url.QueryEscape(next), "").Header().Get("Location"); loc != "/" {
			t.Errorf("switch to %q went to %q", next, loc)
		}
	}

	// The editor shows both versions.
	var id string
	a.store.view(func(d *Data) { id = d.postBySlug("turntables").ID })
	b = get("/admin/posts/"+id, "", ck).Body.String()
	if !strings.Contains(b, `name="body_ru"`) || !strings.Contains(b, "Русский текст.") || !strings.Contains(b, "English text.") {
		t.Errorf("editor\n%s", b)
	}
	// Emptying a tab removes the translation.
	post("/admin/posts/"+id, url.Values{"lang": {"en"}, "slug": {"turntables"}, "title_en": {"Turntables"}, "body_en": {"English text."}})
	a.store.view(func(d *Data) {
		if d.postBySlug("turntables").Translation != nil {
			t.Error("translation kept after emptying its tab")
		}
	})

	// Menu labels and the description follow the reader.
	a.store.update(func(d *Data) error {
		d.Settings.Nav = parseLinks("About | Обо мне | /about/\nMusic | /tag/music/")
		d.Settings.Description, d.Settings.DescriptionAlt = "Records", "Пластинки"
		return nil
	})
	b = get("/", "ru").Body.String()
	if !strings.Contains(b, ">Обо мне<") || !strings.Contains(b, ">Music<") || !strings.Contains(b, "Пластинки") {
		t.Errorf("ru menu\n%s", b)
	}
	if b := get("/", "en").Body.String(); !strings.Contains(b, ">About<") || !strings.Contains(b, "Records") {
		t.Errorf("en menu\n%s", b)
	}
	if got := linksText(parseLinks("About | Обо мне | /about/")); got != "About | Обо мне | /about/\n" {
		t.Errorf("links round trip: %q", got)
	}
}

func TestMergePosts(t *testing.T) {
	a := testApp(t)
	now := time.Now().Add(-time.Hour)
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts,
			&Post{ID: "a", Slug: "vinyl", Title: "Vinyl", Body: "en", Lang: "en", Tags: []string{"Vinyl"}, PublishedAt: now},
			&Post{ID: "b", Slug: "vinyl-ru", Title: "Винил", Body: "ru ![](/img/2025/11/x.jpg)", Lang: "ru", Tags: []string{"Vinyl", "Музыка"}, Cover: "/img/c.jpg", PublishedAt: now},
		)
		return nil
	})
	if err := a.store.update(func(d *Data) error { return mergePosts(d, "vinyl", "vinyl") }); err == nil {
		t.Error("merged a post into itself")
	}
	if err := a.store.update(func(d *Data) error { return mergePosts(d, "vinyl", "vinyl-ru") }); err != nil {
		t.Fatal(err)
	}
	a.store.view(func(d *Data) {
		p := d.postBySlug("vinyl")
		if len(d.Posts) != 1 || p.Translation == nil || p.Translation.Title != "Винил" || p.Cover != "/img/c.jpg" || len(p.Tags) != 2 {
			t.Errorf("merged post: %+v %+v", p, p.Translation)
		}
		if d.imageUses("2025/11/x.jpg") != 1 {
			t.Error("an image used only by the translation counts as unused")
		}
	})
	for _, path := range []string{"/vinyl-ru/", "/vinyl-ru"} {
		rec := do(t, a, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/vinyl/?lang=ru" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if err := a.store.update(func(d *Data) error { return mergePosts(d, "vinyl", "vinyl-ru") }); err == nil {
		t.Error("merged a post that is gone")
	}
}

func TestPostNeighbours(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	day := 24 * time.Hour
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts,
			&Post{ID: "1", Slug: "first", Title: "First", Body: "x", Lang: "en", PublishedAt: now.Add(-5 * day)},
			&Post{ID: "2", Slug: "about", Title: "About", Body: "x", Lang: "en", Page: true, PublishedAt: now.Add(-4 * day)},
			&Post{ID: "3", Slug: "second", Title: "Second", Body: "x", Lang: "en", PublishedAt: now.Add(-3 * day),
				Translation: &Translation{Title: "Второй", Body: "x"}},
			&Post{ID: "4", Slug: "draft", Title: "Draft", Body: "x", Lang: "en", Draft: true},
			&Post{ID: "5", Slug: "third", Title: "Third", Body: "x", Lang: "en", PublishedAt: now.Add(-1 * day)},
			&Post{ID: "6", Slug: "later", Title: "Later", Body: "x", Lang: "en", PublishedAt: now.Add(day)},
		)
		return nil
	})
	nav := func(slug, accept string) (older, newer string) {
		req := httptest.NewRequest("GET", "/"+slug+"/", nil)
		req.Header.Set("Accept-Language", accept)
		b := do(t, a, req).Body.String()
		_, rest, ok := strings.Cut(b, `class="post-nav"`)
		if !ok {
			return "", ""
		}
		olderPart, newerPart, _ := strings.Cut(rest, `post-nav-side newer`)
		title := func(s string) string {
			_, after, ok := strings.Cut(s, `class="tile-link" href="`)
			if !ok {
				return ""
			}
			href, _, _ := strings.Cut(after, `"`)
			return href
		}
		return title(olderPart), title(newerPart)
	}
	for _, c := range []struct{ slug, older, newer string }{
		{"third", "/second/", ""},
		{"second", "/first/", "/third/"},
		{"first", "", "/second/"},
		{"about", "", ""},
	} {
		if o, n := nav(c.slug, "en"); o != c.older || n != c.newer {
			t.Errorf("%s: older %q newer %q, want %q %q", c.slug, o, n, c.older, c.newer)
		}
	}
	// The neighbour's title is in the reader's language.
	req := httptest.NewRequest("GET", "/first/", nil)
	req.Header.Set("Accept-Language", "ru")
	if b := do(t, a, req).Body.String(); !strings.Contains(b, ">Второй</a>") || !strings.Contains(b, "Позже →") {
		t.Errorf("ru neighbour\n%s", b)
	}
	// Every public page carries the script and the button.
	if b := do(t, a, httptest.NewRequest("GET", "/", nil)).Body.String(); !strings.Contains(b, `src="/static/site.js`) || !strings.Contains(b, `class="to-top"`) {
		t.Error("site.js or the back-to-top button missing")
	}
}

// fakeBluesky answers the three XRPC calls the blog makes and records them.
type fakeBluesky struct {
	srv     *httptest.Server
	fail    bool
	records []map[string]any
	blobs   []string // content types
}

func newFakeBluesky(t *testing.T) *fakeBluesky {
	f := &fakeBluesky{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if f.fail || in["identifier"] != "blog.example.com" || in["password"] != "app-pass" {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"AuthenticationRequired","message":"Invalid identifier or password"}`))
				return
			}
			w.Write([]byte(`{"accessJwt":"jwt","refreshJwt":"r","handle":"blog.example.com","did":"did:plc:test"}`))
		case "/xrpc/com.atproto.repo.uploadBlob":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.blobs = append(f.blobs, r.Header.Get("Content-Type"))
			w.Write([]byte(`{"blob":{"$type":"blob","ref":{"$link":"bafy"},"mimeType":"image/jpeg","size":10}}`))
		case "/xrpc/com.atproto.repo.createRecord":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			f.records = append(f.records, in)
			w.Write([]byte(`{"uri":"at://did:plc:test/app.bsky.feed.post/3abc","cid":"bafyrei"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestBlueskySyndication(t *testing.T) {
	a := testApp(t)
	fake := newFakeBluesky(t)
	a.bsky = newBluesky(Config{BlueskyHandle: "blog.example.com", BlueskyPassword: "app-pass", BlueskyService: fake.srv.URL})
	ck := adminCookieFor(a)
	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(ck)
		return do(t, a, req)
	}
	get := func(slug string) Post {
		var p Post
		a.store.view(func(d *Data) { p = *d.postBySlug(slug) })
		return p
	}

	// A cover to send as the card's picture.
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1600, 900)), nil)
	im, err := a.addImage("cover.jpg", buf.Bytes(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Without the box nothing is sent.
	post("/admin/posts", url.Values{"title_en": {"Quiet"}, "body_en": {"x"}, "action": {"publish"}})
	if len(fake.records) != 0 {
		t.Fatal("sent without the box")
	}

	// Publishing with the box sends the card once.
	rec := post("/admin/posts", url.Values{"title_en": {"Loud"}, "body_en": {"Some **text**."}, "excerpt_en": {"An excerpt"},
		"cover": {"/img/" + im.Key}, "title_ru": {"Громко"}, "body_ru": {"Текст"}, "bluesky": {"1"}, "action": {"publish"}})
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?m=bsky_sent") {
		t.Fatalf("publish: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(fake.records) != 1 || len(fake.blobs) != 1 || fake.blobs[0] != "image/jpeg" {
		t.Fatalf("records %d, blobs %v", len(fake.records), fake.blobs)
	}
	r := fake.records[0]
	record := r["record"].(map[string]any)
	ext := record["embed"].(map[string]any)["external"].(map[string]any)
	if r["repo"] != "did:plc:test" || record["text"] != blueskyText || ext["uri"] != "https://blog.example.com/loud/" ||
		ext["title"] != "Loud" || ext["description"] != "An excerpt" || ext["thumb"] == nil || record["langs"].([]any)[0] != "en" {
		t.Errorf("record: %+v", r)
	}
	p := get("loud")
	if p.Bluesky == nil || p.Bluesky.URL != "https://bsky.app/profile/did:plc:test/post/3abc" || p.BlueskyError != "" {
		t.Errorf("recorded: %+v %q", p.Bluesky, p.BlueskyError)
	}
	// Saving again never sends twice, even with the box.
	post("/admin/posts/"+p.ID, url.Values{"title_en": {"Loud"}, "body_en": {"fixed"}, "slug": {"loud"}, "bluesky": {"1"}})
	a.sendDue()
	if len(fake.records) != 1 {
		t.Fatalf("sent twice: %d", len(fake.records))
	}
	if b := do(t, a, func() *http.Request {
		req := httptest.NewRequest("GET", "/admin/posts/"+p.ID, nil)
		req.AddCookie(ck)
		return req
	}()).Body.String(); !strings.Contains(b, "bsky.app/profile/did:plc:test/post/3abc") || strings.Contains(b, `name="bluesky"`) {
		t.Errorf("editor after sending\n%s", b)
	}

	// A draft or a scheduled post waits until it is live.
	post("/admin/posts", url.Values{"title_en": {"Draft"}, "body_en": {"x"}, "bluesky": {"1"}})
	future := time.Now().Add(2 * time.Hour)
	post("/admin/posts", url.Values{"title_en": {"Soon"}, "body_en": {"x"}, "bluesky": {"1"}, "action": {"publish"},
		"published_at": {future.Format("2006-01-02T15:04")}})
	a.sendDue()
	if len(fake.records) != 1 {
		t.Fatalf("draft or scheduled post sent early: %d", len(fake.records))
	}
	a.now = func() time.Time { return future.Add(time.Minute) }
	a.sendDue()
	if len(fake.records) != 2 || get("soon").Bluesky == nil {
		t.Fatalf("scheduled post not sent when due: %d", len(fake.records))
	}
	a.now = time.Now

	// A failure is recorded, not retried by the timer, retried on save.
	fake.fail = true
	rec = post("/admin/posts", url.Values{"title_en": {"Broken"}, "body_en": {"x"}, "bluesky": {"1"}, "action": {"publish"}})
	if !strings.HasSuffix(rec.Header().Get("Location"), "?m=bsky_failed") || !strings.Contains(get("broken").BlueskyError, "AuthenticationRequired") {
		t.Fatalf("failure: %s %q", rec.Header().Get("Location"), get("broken").BlueskyError)
	}
	fake.fail = false
	a.sendDue()
	if len(fake.records) != 2 {
		t.Fatal("the timer retried a failed post")
	}
	id := get("broken").ID
	post("/admin/posts/"+id, url.Values{"title_en": {"Broken"}, "body_en": {"x"}, "slug": {"broken"}, "bluesky": {"1"}})
	if len(fake.records) != 3 || get("broken").Bluesky == nil || get("broken").BlueskyError != "" {
		t.Fatalf("retry on save: %d %q", len(fake.records), get("broken").BlueskyError)
	}
}

// The published RFC 6238 SHA-1 vectors, last six of the eight digits: the
// only proof that this is TOTP and not merely self-consistent.
func TestTOTPMatchesTheRFCVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for _, c := range []struct {
		unix int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got, err := totpCode(secret, time.Unix(c.unix, 0)); err != nil || got != c.want {
			t.Errorf("T=%d: %s %v, want %s", c.unix, got, err, c.want)
		}
	}
	at := time.Unix(1234567890, 0)
	code, _ := totpCode(secret, at)
	if checkTOTP("gezd gnbv gy3t qojq GEZDGNBVGY3TQOJQ", code, at, 0) == 0 {
		t.Error("spaced lower-case secret refused")
	}
	if step := checkTOTP(secret, code, at, 0); step == 0 || checkTOTP(secret, code, at, step) != 0 {
		t.Error("a used code was accepted again")
	}
	if checkTOTP(secret, code, at.Add(90*time.Second), 0) != 0 {
		t.Error("a code three steps old was accepted")
	}
}

func TestSecurity(t *testing.T) {
	a := testApp(t)
	clock := time.Now()
	a.now = func() time.Time { return clock }
	form := func(path string, v url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return do(t, a, req)
	}
	session := func(rec *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range rec.Result().Cookies() {
			if c.Name == adminCookie {
				return c
			}
		}
		t.Fatalf("no session cookie: %d %v", rec.Code, rec.Header())
		return nil
	}
	in := func(c *http.Cookie) bool {
		req := httptest.NewRequest("GET", "/admin/", nil)
		req.AddCookie(c)
		return do(t, a, req).Code == 200
	}
	a.loginLimit = newLimiter(1000, time.Minute)

	// User name and password from .env.
	if rec := form("/admin/login", url.Values{"user": {"nobody"}, "password": {"correct horse"}}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong user: %d", rec.Code)
	}
	phone := session(form("/admin/login", url.Values{"user": {"Admin"}, "password": {"correct horse"}}))
	laptop := session(form("/admin/login", url.Values{"user": {"admin"}, "password": {"correct horse"}}))
	if !in(phone) || !in(laptop) {
		t.Fatal("sessions not valid")
	}

	// A new user name and password need the current one; other devices go.
	if rec := form("/admin/security/account", url.Values{"user": {"stas"}, "new": {"a much longer one"}, "new2": {"a much longer one"}, "current": {"wrong"}}, laptop); !strings.Contains(rec.Header().Get("Location"), "e=sec_bad_current") {
		t.Fatalf("change without the current password: %s", rec.Header().Get("Location"))
	}
	if rec := form("/admin/security/account", url.Values{"user": {"stas"}, "new": {"short"}, "new2": {"short"}, "current": {"correct horse"}}, laptop); !strings.Contains(rec.Header().Get("Location"), "e=sec_short") {
		t.Fatalf("short password: %s", rec.Header().Get("Location"))
	}
	rec := form("/admin/security/account", url.Values{"user": {"stas"}, "new": {"a much longer one"}, "new2": {"a much longer one"}, "current": {"correct horse"}}, laptop)
	laptop = session(rec)
	if in(phone) || !in(laptop) {
		t.Fatal("the change did not sign the other device out, or this one")
	}
	if rec := form("/admin/login", url.Values{"user": {"admin"}, "password": {"correct horse"}}); rec.Code != http.StatusUnauthorized {
		t.Fatal("the old account still signs in")
	}

	// 2FA: the setup page shows a secret; the first code turns it on.
	form("/admin/security/totp/start", nil, laptop)
	req := httptest.NewRequest("GET", "/admin/security", nil)
	req.AddCookie(laptop)
	page := do(t, a, req).Body.String()
	_, rest, _ := strings.Cut(page, `<code class="secret">`)
	secret, _, _ := strings.Cut(rest, "<")
	if secret == "" || !strings.Contains(page, `data-qr="otpauth://totp/blog.example.com:stas?`) {
		t.Fatalf("setup page\n%s", page)
	}
	phone = session(form("/admin/login", url.Values{"user": {"stas"}, "password": {"a much longer one"}}))
	code, _ := totpCode(secret, clock)
	if rec := form("/admin/security/totp/enable", url.Values{"code": {"000000"}}, laptop); !strings.Contains(rec.Header().Get("Location"), "e=sec_bad_code") {
		t.Fatal("a wrong first code turned 2FA on")
	}
	laptop = session(form("/admin/security/totp/enable", url.Values{"code": {code}}, laptop))
	if in(phone) || !in(laptop) || !a.totpOn() {
		t.Fatal("turning 2FA on did not sign the other device out")
	}

	// Signing in now takes a code, once.
	clock = clock.Add(30 * time.Second)
	code, _ = totpCode(secret, clock)
	if rec := form("/admin/login", url.Values{"user": {"stas"}, "password": {"a much longer one"}}); rec.Code != http.StatusUnauthorized {
		t.Fatal("signed in without a code")
	}
	form("/admin/login", url.Values{"user": {"stas"}, "password": {"a much longer one"}, "code": {code}})
	if rec := form("/admin/login", url.Values{"user": {"stas"}, "password": {"a much longer one"}, "code": {code}}); rec.Code != http.StatusUnauthorized {
		t.Fatal("the same code worked twice")
	}

	// The account survives a restart; a broken auth.json stops the blog.
	b, err := newApp(a.cfg)
	if err != nil || !b.totpOn() {
		t.Fatalf("reload: %v", err)
	}
	if u, _, _ := b.credentials(); u != "stas" {
		t.Errorf("reloaded user %q", u)
	}
	os.WriteFile(filepath.Join(a.cfg.DataDir, "auth.json"), []byte("{broken"), 0o600)
	if _, err := newApp(a.cfg); err == nil {
		t.Error("started with a broken auth.json")
	}
	os.Remove(filepath.Join(a.cfg.DataDir, "auth.json"))

	// Turning 2FA off asks for the password and a code.
	clock = clock.Add(30 * time.Second)
	code, _ = totpCode(secret, clock)
	if rec := form("/admin/security/totp/disable", url.Values{"current": {"a much longer one"}}, laptop); !strings.Contains(rec.Header().Get("Location"), "e=sec_bad_code") {
		t.Fatalf("2FA off without a code: %s", rec.Header().Get("Location"))
	}
	form("/admin/security/totp/disable", url.Values{"current": {"a much longer one"}, "code": {code}}, laptop)
}

func TestAdminOff(t *testing.T) {
	t.Setenv("ADMIN_UI", "off")
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("a standby needs no password: %v", err)
	}
	cfg.DataDir = t.TempDir()
	a, err := newApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(t, a, httptest.NewRequest("GET", "/", nil)); rec.Code != http.StatusOK {
		t.Fatalf("home page: %d", rec.Code)
	}
	for _, path := range []string{"/admin/", "/admin/login", "/admin/settings"} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(adminCookieFor(a))
		if rec := do(t, a, req); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, rec.Code)
		}
	}
	form := url.Values{"user": {"admin"}, "password": {"x"}}
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := do(t, a, req); rec.Code == http.StatusOK || rec.Code == http.StatusSeeOther {
		t.Errorf("POST /admin/login answered %d", rec.Code)
	}
}

func TestFollowBox(t *testing.T) {
	a := testApp(t)
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts,
			&Post{ID: "1", Slug: "post", Title: "Post", Body: "x", Lang: "en", PublishedAt: time.Now().Add(-time.Hour)},
			&Post{ID: "2", Slug: "about", Title: "About", Body: "x", Lang: "en", Page: true, PublishedAt: time.Now().Add(-time.Hour)},
		)
		return nil
	})
	get := func(path, accept string) string {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Accept-Language", accept)
		return do(t, a, req).Body.String()
	}
	if b := get("/post/", "en"); strings.Contains(b, `class="follow"`) {
		t.Error("box shown with no profiles set")
	}

	form := url.Values{"title": {"Blog"}, "lang": {"en"}, "posts_per_page": {"10"},
		"bluesky": {" https://bsky.app/profile/me.example "}, "mastodon": {"https://mastodon.social/@me"}, "x": {"javascript:alert(1)"}}
	req := httptest.NewRequest("POST", "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(adminCookieFor(a))
	if rec := do(t, a, req); rec.Code != http.StatusSeeOther {
		t.Fatalf("settings: %d", rec.Code)
	}
	var s Settings
	a.store.view(func(d *Data) { s = d.Settings })
	if s.Bluesky != "https://bsky.app/profile/me.example" || s.Mastodon != "https://mastodon.social/@me" || s.X != "" {
		t.Fatalf("stored %q %q %q", s.Bluesky, s.Mastodon, s.X)
	}

	b := get("/post/", "en")
	for _, want := range []string{
		`Comments are welcome on <a href="https://bsky.app/profile/me.example" rel="noopener" target="_blank"><svg class="icon"`,
		`</svg>Bluesky</a> and <a href="https://mastodon.social/@me" rel="me noopener" target="_blank"><svg class="icon"`,
		`Follow the blog: <a href="/rss/"><svg class="icon"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("en box lacks %q", want)
		}
	}
	if strings.Contains(b, "javascript:") {
		t.Error("a non-http profile got through")
	}
	if b := get("/post/", "ru"); !strings.Contains(b, `</svg>Bluesky</a> и <a`) || !strings.Contains(b, `Подписаться на блог: <a href="/rss/ru/">`) {
		t.Error("ru box wrong")
	}
	if b := get("/about/", "en"); strings.Contains(b, `class="follow"`) {
		t.Error("box shown on a page")
	}
	// The footer links the Mastodon profile with rel="me" on every page,
	// the home page included: that is the page a profile field points at.
	if b := get("/", "en"); !strings.Contains(b, `<a href="https://mastodon.social/@me" rel="me" target="_blank">Mastodon</a>`) {
		t.Error("footer lacks the rel=me link")
	}
	// The same profile credits the author on Mastodon's link previews.
	if b := get("/post/", "en"); !strings.Contains(b, `<meta name="fediverse:creator" content="@me@mastodon.social">`) {
		t.Error("head lacks fediverse:creator")
	}
	for in, want := range map[string]string{
		"https://mastodon.social/@me":   "@me@mastodon.social",
		"https://mastodon.social/@me/":  "@me@mastodon.social",
		"https://mas.to/@a_b":           "@a_b@mas.to",
		"https://mastodon.social/me":    "",
		"https://mastodon.social/@":     "",
		"https://mastodon.social/@me/1": "",
		"":                              "",
	} {
		if got := fediverseHandle(in); got != want {
			t.Errorf("fediverseHandle(%q) = %q, want %q", in, got, want)
		}
	}

	// Three profiles: a comma, then "and".
	got := socialLinks(Settings{Bluesky: "b", Mastodon: "m", X: "x"}, "en")
	if len(got) != 3 || got[0].Sep != "" || got[1].Sep != ", " || got[2].Sep != " and " || got[2].Name != "X-social" {
		t.Errorf("separators %+v", got)
	}
}

func TestTagAdmin(t *testing.T) {
	a := testApp(t)
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts,
			&Post{ID: "1", Slug: "a", Title: "A", Body: "x", Lang: "en", Tags: []string{"Vinyl", "Music"}},
			&Post{ID: "2", Slug: "b", Title: "B", Body: "x", Lang: "en", Tags: []string{"vinyl", "Canada"}, Draft: true},
		)
		return nil
	})
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(adminCookieFor(a))
		return do(t, a, req)
	}
	tags := func() map[string][]string {
		m := map[string][]string{}
		a.store.view(func(d *Data) {
			for _, p := range d.Posts {
				m[p.ID] = p.Tags
			}
		})
		return m
	}

	var rows []tagRow
	a.store.view(func(d *Data) { rows = d.tagRows() })
	if len(rows) != 3 || rows[0] != (tagRow{Name: "Vinyl", Slug: "vinyl", Count: 2}) {
		t.Fatalf("rows %+v", rows)
	}

	// Renaming into an existing tag merges, with no duplicate.
	if rec := post("/admin/tags/rename", url.Values{"slug": {"vinyl"}, "name": {"Music"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("rename: %d", rec.Code)
	}
	if m := tags(); !slices.Equal(m["1"], []string{"Music"}) || !slices.Equal(m["2"], []string{"Music", "Canada"}) {
		t.Fatalf("after rename %v", m)
	}

	if rec := post("/admin/tags/rename", url.Values{"slug": {"music"}, "name": {"a, b"}}); !strings.Contains(rec.Header().Get("Location"), "tag_name_failed") {
		t.Fatalf("comma accepted: %s", rec.Header().Get("Location"))
	}

	post("/admin/tags/delete", url.Values{"slug": {"music"}})
	if m := tags(); len(m["1"]) != 0 || !slices.Equal(m["2"], []string{"Canada"}) {
		t.Fatalf("after delete %v", m)
	}

	req := httptest.NewRequest("GET", "/admin/settings", nil)
	req.AddCookie(adminCookieFor(a))
	if b := do(t, a, req).Body.String(); !strings.Contains(b, `<a href="/tag/canada/" target="_blank">Canada</a>`) {
		t.Error("settings page has no tag list")
	}

	req = httptest.NewRequest("GET", "/admin/posts/1", nil)
	req.AddCookie(adminCookieFor(a))
	b := do(t, a, req).Body.String()
	if !strings.Contains(b, `<li role="option" data-tag="Canada">Canada</li>`) {
		t.Error("editor has no tag menu")
	}
	// Prompt texts stay plain: html/template escapes attributes named *url*
	// as addresses ("Link%20address").
	if !strings.Contains(b, `data-link-ask="Link address"`) {
		t.Error("link prompt text is escaped")
	}
}

func TestExternalLinksNewTab(t *testing.T) {
	md := newMarkdown(nil, "blog.example.com")
	h, err := md.render("[out](https://example.org/a) [home](https://blog.example.com/x/) [rel](/about/) [top](#a) <https://example.net> www.example.com [mail](mailto:me@example.org)\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<a href="https://example.org/a" target="_blank" rel="noopener">out</a>`,
		`<a href="https://blog.example.com/x/">home</a>`,
		`<a href="/about/">rel</a>`,
		`<a href="#a">top</a>`,
		`<a href="https://example.net" target="_blank" rel="noopener">https://example.net</a>`,
		`<a href="http://www.example.com" target="_blank" rel="noopener">www.example.com</a>`,
		`<a href="mailto:me@example.org">mail</a>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %s in\n%s", want, h)
		}
	}

	a := testApp(t)
	a.store.update(func(d *Data) error {
		d.Settings.Nav = parseLinks("About | /about/\nShop | https://shop.example.org/")
		return nil
	})
	b := do(t, a, httptest.NewRequest("GET", "/", nil)).Body.String()
	if !strings.Contains(b, `<a href="/about/">About</a>`) || !strings.Contains(b, `<a href="https://shop.example.org/" target="_blank" rel="noopener">Shop</a>`) {
		t.Error("menu links: new tab only for other sites")
	}
}

func TestUploadNamedCover(t *testing.T) {
	a := testApp(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "halloween-candies-cover")
	fw, _ := mw.CreateFormFile("file", "IMG_1234.PNG")
	fw.Write(encodeJPEG(t, 800, 600, 0))
	mw.Close()
	req := httptest.NewRequest("POST", "/admin/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(adminCookieFor(a))
	rec := do(t, a, req)
	var res struct{ URL string }
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != 200 || !strings.HasSuffix(res.URL, "/halloween-candies-cover.jpg") {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
}

func TestPagerTimeline(t *testing.T) {
	a := testApp(t)
	a.store.update(func(d *Data) error {
		d.Settings.PostsPerPage = 1
		for i, slug := range []string{"one", "two", "three"} {
			d.Posts = append(d.Posts, &Post{ID: slug, Slug: slug, Title: slug, Body: "x", Lang: "en", PublishedAt: time.Now().Add(-time.Duration(i+1) * time.Hour)})
		}
		return nil
	})
	b := do(t, a, httptest.NewRequest("GET", "/page/2/", nil)).Body.String()
	older, newer := strings.Index(b, "← Older posts"), strings.Index(b, "Newer posts →")
	if older < 0 || newer < 0 || older > newer {
		t.Errorf("pager: older at %d, newer at %d", older, newer)
	}
}

func TestPostGallery(t *testing.T) {
	a := testApp(t)
	a.store.update(func(d *Data) error {
		d.Images["2026/10/c.jpg"] = &Image{Key: "2026/10/c.jpg", Width: 1400, Height: 700, Widths: []int{600, 1000, 1400}}
		d.Posts = append(d.Posts,
			&Post{ID: "1", Slug: "with", Title: "With", Body: "x", Cover: "/img/2026/10/c.jpg", Lang: "en", PublishedAt: time.Now().Add(-time.Hour)},
			&Post{ID: "2", Slug: "without", Title: "Without", Body: "x", Lang: "en", PublishedAt: time.Now().Add(-2 * time.Hour)},
		)
		return nil
	})
	b := do(t, a, httptest.NewRequest("GET", "/with/", nil)).Body.String()
	for _, want := range []string{
		`<a class="zoom" href="/img/2026/10/c.jpg" data-pswp-width="1400" data-pswp-height="700" data-pswp-srcset="/img/2026/10/c-w600.jpg 600w, /img/2026/10/c-w1000.jpg 1000w, /img/2026/10/c.jpg 1400w"><img`,
		`<script type="module" src="/static/gallery.js?v=`,
		`/static/photoswipe/photoswipe.css?v=`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("missing %s", want)
		}
	}
	if b := do(t, a, httptest.NewRequest("GET", "/without/", nil)).Body.String(); strings.Contains(b, "gallery.js") {
		t.Error("gallery loaded on a post without images")
	}
	for _, f := range []string{"/static/gallery.js", "/static/photoswipe/photoswipe-lightbox.esm.min.js", "/static/photoswipe/photoswipe.esm.min.js", "/static/photoswipe/photoswipe.css"} {
		if rec := do(t, a, httptest.NewRequest("GET", f, nil)); rec.Code != 200 {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}
}

func TestStaticCache(t *testing.T) {
	a := testApp(t)
	for path, want := range map[string]string{
		"/static/style.css?v=" + version: "public, max-age=31536000, immutable",
		"/static/style.css?v=old":        "public, max-age=86400",
		"/static/style.css":              "public, max-age=86400",
	} {
		w := do(t, a, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != want {
			t.Errorf("%s: %d %q, want %q", path, w.Code, w.Header().Get("Cache-Control"), want)
		}
	}
}

func TestLangURLsAndSitemap(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	day := 24 * time.Hour
	a.store.update(func(d *Data) error {
		d.Posts = append(d.Posts,
			&Post{ID: "1", Slug: "both", Title: "Both", Body: "x", Lang: "en", PublishedAt: now.Add(-2 * day), UpdatedAt: now.Add(-2 * day),
				Translation: &Translation{Title: "Оба", Body: "x"}},
			&Post{ID: "2", Slug: "one", Title: "One", Body: "x", Lang: "en", PublishedAt: now.Add(-day), UpdatedAt: now.Add(-day)},
			&Post{ID: "3", Slug: "draft", Title: "Draft", Body: "x", Lang: "en", Draft: true},
			&Post{ID: "4", Slug: "later", Title: "Later", Body: "x", Lang: "en", PublishedAt: now.Add(day)},
		)
		return nil
	})
	const o = "https://blog.example.com"
	alts := `<link rel="alternate" hreflang="en" href="` + o + `/both/?lang=en">` + "\n" +
		`<link rel="alternate" hreflang="ru" href="` + o + `/both/?lang=ru">` + "\n" +
		`<link rel="alternate" hreflang="x-default" href="` + o + `/both/">`
	for path, want := range map[string][]string{
		"/both/":          {`<link rel="canonical" href="` + o + `/both/">`, alts},
		"/both/?lang=ru":  {`<link rel="canonical" href="` + o + `/both/?lang=ru">`, alts},
		"/one/?lang=ru":   {`<link rel="canonical" href="` + o + `/one/">`},
		"/":               {`<link rel="canonical" href="` + o + `/">`, `hreflang="ru" href="` + o + `/?lang=ru"`},
		"/tag/x/?lang=en": nil, // no such tag: a 404 has no canonical
	} {
		b := do(t, a, httptest.NewRequest("GET", path, nil)).Body.String()
		for _, s := range want {
			if !strings.Contains(b, s) {
				t.Errorf("%s: missing %s", path, s)
			}
		}
		if want == nil && strings.Contains(b, `rel="canonical"`) {
			t.Errorf("%s: canonical on a 404", path)
		}
		if path == "/one/?lang=ru" && strings.Contains(b, `hreflang="x-default"`) {
			t.Errorf("%s: alternates on a post in one language", path)
		}
	}

	b := do(t, a, httptest.NewRequest("GET", "/robots.txt", nil)).Body.String()
	if !strings.Contains(b, "Sitemap: "+o+"/sitemap.xml") {
		t.Errorf("robots.txt\n%s", b)
	}
	rec := do(t, a, httptest.NewRequest("GET", "/sitemap.xml", nil))
	b = rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("sitemap %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, s := range []string{
		"<url><loc>" + o + "/one/</loc><lastmod>" + now.Add(-day).UTC().Format("2006-01-02") + "</lastmod></url>",
		"<url><loc>" + o + "/both/?lang=ru</loc>",
		`<xhtml:link rel="alternate" hreflang="x-default" href="` + o + `/both/"/>`,
		"<url><loc>" + o + "/?lang=en</loc>",
	} {
		if !strings.Contains(b, s) {
			t.Errorf("sitemap missing %s", s)
		}
	}
	for _, s := range []string{"/draft/", "/later/"} {
		if strings.Contains(b, s) {
			t.Errorf("sitemap lists %s", s)
		}
	}
	if strings.Count(b, "<url>") != 7 { // home and both ×3, one
		t.Errorf("sitemap has %d entries\n%s", strings.Count(b, "<url>"), b)
	}
	var x struct{}
	if err := xml.Unmarshal([]byte(b), &x); err != nil {
		t.Errorf("sitemap is not XML: %v", err)
	}
}
