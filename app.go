package main

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed templates static
var assets embed.FS

type App struct {
	cfg        Config
	store      *Store
	adminHash  pwHash
	sessionKey []byte
	imagesDir  string
	md         *markdown
	tpl        map[string]*template.Template
	loginLimit *limiter
	now        func() time.Time
	// uploadMu serialises uploads, so two images never take the same name.
	uploadMu sync.Mutex
	// bsky announces posts on Bluesky; nil when not set up. sendMu keeps
	// a post from being sent twice by the editor and the timer at once.
	bsky   *bluesky
	sendMu sync.Mutex
	// auth is DATA_DIR/auth.json: the account as changed on the Security
	// page. pending is a 2FA secret shown for scanning, not yet confirmed.
	authMu    sync.RWMutex
	auth      authState
	authPath  string
	pending   string
	pendingAt time.Time
}

func newApp(cfg Config) (*App, error) {
	imagesDir := filepath.Join(cfg.DataDir, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		return nil, err
	}
	store, err := openStore(filepath.Join(cfg.DataDir, "blog.json"))
	if err != nil {
		return nil, err
	}
	key, err := loadSessionKey(filepath.Join(cfg.DataDir, "session.key"))
	if err != nil {
		return nil, err
	}
	hash, _ := parseHash(cfg.AdminHash) // validated in loadConfig
	if cfg.AdminUser == "" {
		cfg.AdminUser = "admin"
	}
	authPath := filepath.Join(cfg.DataDir, "auth.json")
	auth, err := loadAuth(authPath)
	if err != nil {
		return nil, err
	}
	tpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	var host string
	if u, err := url.Parse(cfg.BaseURL); err == nil {
		host = u.Hostname()
	}
	return &App{
		cfg:        cfg,
		store:      store,
		adminHash:  hash,
		sessionKey: key,
		imagesDir:  imagesDir,
		md:         newMarkdown(store.image, host),
		tpl:        tpl,
		loginLimit: newLimiter(5, 15*time.Minute),
		now:        time.Now,
		bsky:       newBluesky(cfg),
		auth:       auth,
		authPath:   authPath,
	}, nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", staticCache(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.HandleFunc("GET /img/{path...}", a.handleImage)
	mux.HandleFunc("GET /content/images/{path...}", a.handleOldImage)
	mux.HandleFunc("GET /favicon.ico", a.handleFavicon)
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nDisallow: /admin/\n\nSitemap: " + a.origin(r) + "/sitemap.xml\n"))
	})
	mux.HandleFunc("GET /sitemap.xml", a.handleSitemap)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })

	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /page/{n}/{$}", a.handleIndex)
	mux.HandleFunc("GET /tag/{tag}/{$}", a.handleIndex)
	mux.HandleFunc("GET /tag/{tag}/page/{n}/{$}", a.handleIndex)
	mux.HandleFunc("GET /rss/{$}", a.handleRSS)
	mux.HandleFunc("GET /rss/{lang}/{$}", a.handleRSS)
	mux.HandleFunc("GET /rss", redirectTo("/rss/"))
	mux.HandleFunc("GET /lang/{code}", a.handleLang)
	// Posts and pages: /{slug}/. A {slug} pattern would clash with /static/
	// and /img/, so the catch-all route dispatches.
	mux.HandleFunc("GET /", a.handleSlugPath)

	if a.cfg.NoAdmin {
		return a.secure(mux)
	}
	mux.HandleFunc("GET /admin", redirectTo("/admin/"))
	mux.HandleFunc("GET /admin/login", a.handleLoginForm)
	mux.HandleFunc("POST /admin/login", a.handleLogin)
	mux.HandleFunc("POST /admin/logout", a.handleLogout)
	mux.HandleFunc("GET /admin/lang/{code}", a.handleLang)
	mux.HandleFunc("GET /admin/{$}", a.requireAdmin(a.handleAdminPosts))
	mux.HandleFunc("GET /admin/new", a.requireAdmin(a.handleNewForm))
	mux.HandleFunc("POST /admin/posts", a.requireAdmin(a.handleSave))
	mux.HandleFunc("GET /admin/posts/{id}", a.requireAdmin(a.handleEditForm))
	mux.HandleFunc("POST /admin/posts/{id}", a.requireAdmin(a.handleSave))
	mux.HandleFunc("POST /admin/posts/{id}/delete", a.requireAdmin(a.handleDelete))
	mux.HandleFunc("POST /admin/preview", a.requireAdmin(a.handlePreview))
	mux.HandleFunc("POST /admin/upload", a.requireAdmin(a.handleUpload))
	mux.HandleFunc("GET /admin/images", a.requireAdmin(a.handleImages))
	mux.HandleFunc("POST /admin/images/delete", a.requireAdmin(a.handleImageDelete))
	mux.HandleFunc("GET /admin/security", a.requireAdmin(a.handleSecurity))
	mux.HandleFunc("POST /admin/security/account", a.requireAdmin(a.handleAccount))
	mux.HandleFunc("POST /admin/security/totp/start", a.requireAdmin(a.handleTOTPStart))
	mux.HandleFunc("POST /admin/security/totp/enable", a.requireAdmin(a.handleTOTPEnable))
	mux.HandleFunc("POST /admin/security/totp/disable", a.requireAdmin(a.handleTOTPDisable))
	mux.HandleFunc("POST /admin/security/signout", a.requireAdmin(a.handleSignOutOthers))
	mux.HandleFunc("GET /admin/settings", a.requireAdmin(a.handleSettingsForm))
	mux.HandleFunc("POST /admin/settings", a.requireAdmin(a.handleSettings))
	mux.HandleFunc("POST /admin/tags/rename", a.requireAdmin(a.handleTagRename))
	mux.HandleFunc("POST /admin/tags/delete", a.requireAdmin(a.handleTagDelete))

	return a.secure(mux)
}

func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}
}

// staticCache lets browsers keep an asset for a year when its URL names
// this build (?v=<version>, as the templates write it): the next deploy
// changes the URL. Any other request for it is cached for a day.
func staticCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		h.ServeHTTP(w, r)
	})
}

// secure sets the response headers every page shares and rejects cross-site
// form posts.
func (a *App) secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Posts may show images from other sites and YouTube players.
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; frame-src https://www.youtube-nocookie.com https://www.youtube.com https://embed.music.apple.com; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Method == http.MethodPost && !a.sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if a.cfg.BaseURL != "" {
		if b, err := url.Parse(a.cfg.BaseURL); err == nil && strings.EqualFold(u.Host, b.Host) {
			return true
		}
	}
	return false
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// origin is the scheme and host visitors use, for absolute links.
func (a *App) origin(r *http.Request) string {
	if a.cfg.BaseURL != "" {
		return a.cfg.BaseURL
	}
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handleImage serves uploads. A file never changes under its name, so it
// may be cached for a year; a renamed one redirects to its new name.
func (a *App) handleImage(w http.ResponseWriter, r *http.Request) {
	p := path.Clean("/" + r.PathValue("path"))
	if strings.Contains(p, "..") || strings.HasSuffix(p, ".tmp") {
		http.NotFound(w, r)
		return
	}
	f := filepath.Join(a.imagesDir, filepath.FromSlash(p))
	if st, err := os.Stat(f); err != nil || st.IsDir() {
		// A picture renamed after its post (blog rename-images).
		var to string
		a.store.view(func(d *Data) { to = d.movedImage(strings.TrimPrefix(p, "/")) })
		if to == "" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/img/"+to, http.StatusMovedPermanently)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if strings.HasSuffix(p, ".svg") {
		// An SVG opened on its own is a document: no scripts in it.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	}
	http.ServeFile(w, r, f)
}

// handleOldImage sends Ghost's image URLs to the imported copies.
func (a *App) handleOldImage(w http.ResponseWriter, r *http.Request) {
	old := r.PathValue("path")
	// Ghost's resized copies: size/w600/2025/11/x.jpg
	if rest, ok := strings.CutPrefix(old, "size/"); ok {
		if _, after, ok := strings.Cut(rest, "/"); ok {
			old = after
		}
	}
	var key string
	a.store.view(func(d *Data) { key = d.Redirects["/content/images/"+old] })
	if key == "" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/img/"+key, http.StatusMovedPermanently)
}

func (a *App) handleFavicon(w http.ResponseWriter, r *http.Request) {
	var icon string
	a.store.view(func(d *Data) { icon = d.Settings.Icon })
	if icon != "" {
		http.Redirect(w, r, icon, http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFileFS(w, r, assets, "static/favicon.svg")
}

var templateFuncs = template.FuncMap{
	"t":       t,
	"rssIcon": func() template.HTML { return iconRSS },
	// newTab opens a link to another site in a new tab. Menu links to the
	// blog itself are written as paths, so any full web address is outside.
	"newTab": func(u string) template.HTMLAttr {
		if externalURL(u, "") {
			return ` target="_blank" rel="noopener"`
		}
		return ""
	},
	// tile hands a card to the "tile" template with the cover's sizes
	// attribute, which depends on how wide the tiles are where it is used.
	"tile": func(c card, sizes string) any {
		return struct {
			card
			Sizes string
		}{c, sizes}
	},
	"rows": func(lang string, rows []adminRow) any {
		return struct {
			Lang string
			Rows []adminRow
		}{lang, rows}
	},
}

func loadTemplates() (map[string]*template.Template, error) {
	m := map[string]*template.Template{}
	public := []string{"index", "post", "message"}
	admin := []string{"login", "posts", "edit", "images", "settings", "security"}
	for _, p := range public {
		t, err := template.New(p).Funcs(templateFuncs).ParseFS(assets, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, err
		}
		m[p] = t
	}
	for _, p := range admin {
		t, err := template.New(p).Funcs(templateFuncs).ParseFS(assets, "templates/admin_layout.html", "templates/admin_"+p+".html")
		if err != nil {
			return nil, err
		}
		m["admin_"+p] = t
	}
	return m, nil
}

func (a *App) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := a.tpl[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// One URL serves either language.
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}
