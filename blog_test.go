package main

import (
	"bytes"
	"encoding/json"
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
	})
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

	h, _ = md.render("Inline ![icon](/img/i.png) in text.\n")
	if strings.Contains(h, "<figure") || !strings.Contains(h, `<img src="/img/i.png" alt="icon"`) {
		t.Errorf("inline image:\n%s", h)
	}
}

func TestMarkdownEmbedsAndHeadings(t *testing.T) {
	md := newMarkdown(nil)
	h, _ := md.render("https://www.youtube.com/watch?v=gYc5zrXC52k\n\n[a link](https://youtu.be/gYc5zrXC52k)\n")
	if !strings.Contains(h, `src="https://www.youtube-nocookie.com/embed/gYc5zrXC52k"`) {
		t.Errorf("no embed:\n%s", h)
	}
	if strings.Count(h, "<iframe") != 1 {
		t.Errorf("a [text](url) link must stay a link:\n%s", h)
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
