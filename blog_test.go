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
	rec = post("/admin/posts", url.Values{"title": {"Первая запись"}, "body": {"Hello **world**.\n\n## Part {#part}\n"}, "tags": {"Vinyl, Music"}, "action": {"save"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(rec.Header().Get("Location"), "/admin/posts/"), "?m=saved")
	if rec := do(t, a, httptest.NewRequest("GET", "/pervaya-zapis/", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("draft visible: %d", rec.Code)
	}
	rec = post("/admin/posts/"+id, url.Values{"title": {"Первая запись"}, "slug": {"pervaya-zapis"}, "body": {"Hello **world**.\n"}, "tags": {"Vinyl"}, "lang": {"ru"}, "action": {"publish"}})
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
	rec = post("/admin/posts", url.Values{"title": {"Other"}, "slug": {"pervaya-zapis"}, "body": {"x"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("slug clash: %d", rec.Code)
	}
	rec = post("/admin/posts", url.Values{"title": {"Admin"}, "slug": {"admin"}, "body": {"x"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reserved slug: %d", rec.Code)
	}

	// A post scheduled for later stays hidden.
	future := time.Now().Add(48 * time.Hour).Format("2006-01-02T15:04")
	post("/admin/posts", url.Values{"title": {"Later"}, "body": {"soon"}, "published_at": {future}, "action": {"publish"}})
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
	req = httptest.NewRequest("POST", "/admin/posts", strings.NewReader("title=x"))
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
