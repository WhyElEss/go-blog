package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

// The sign-in: a user name, a password and, once enabled, a code from an
// authenticator app.
//
// ADMIN_PASSWORD_HASH in .env is the first password: it has to exist before
// the blog is public, or the first visitor could set one. Everything after
// that is changed on the Security page and kept in DATA_DIR/auth.json, which
// then wins over .env.
//
// Locked out? On the Pi:
//
//	docker exec blog rm /data/auth.json /data/session.key && docker restart blog
//
// brings back the .env user and password, without the second factor, and
// signs every browser out.

// authState is auth.json.
type authState struct {
	User       string `json:"user,omitempty"`
	Hash       string `json:"password_hash,omitempty"`
	TOTPSecret string `json:"totp_secret,omitempty"`
	// TOTPLast is the step of the last accepted code: each code works once.
	TOTPLast int64 `json:"totp_last_step,omitempty"`
	// Epoch is part of every session cookie. Raising it signs out every
	// browser; the one making the change gets a new cookie.
	Epoch int64 `json:"epoch"`
}

const (
	minPasswordLen = 12
	maxPasswordLen = 200
	// pendingTTL is how long a started 2FA setup waits for its first code.
	pendingTTL = 10 * time.Minute
)

// loadAuth reads auth.json. A file that exists but cannot be read stops the
// blog rather than falling back to one factor without anyone noticing.
func loadAuth(path string) (authState, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return authState{}, nil
	}
	if err != nil {
		return authState{}, err
	}
	var s authState
	if err := json.Unmarshal(b, &s); err != nil {
		return authState{}, fmt.Errorf("%s: %w (delete it to fall back to .env)", path, err)
	}
	if s.Hash != "" {
		if _, err := parseHash(s.Hash); err != nil {
			return authState{}, fmt.Errorf("%s: password_hash: %w", path, err)
		}
	}
	if s.TOTPSecret != "" {
		if _, err := decodeTOTPSecret(s.TOTPSecret); err != nil {
			return authState{}, fmt.Errorf("%s: totp_secret: %w", path, err)
		}
	}
	return s, nil
}

// saveAuthLocked writes auth.json atomically; a.authMu must be held.
func (a *App) saveAuthLocked() error {
	b, err := json.MarshalIndent(a.auth, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.authPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.authPath)
}

// credentials returns the user name, the password hash and the TOTP secret
// in force.
func (a *App) credentials() (user string, hash pwHash, secret string) {
	a.authMu.RLock()
	defer a.authMu.RUnlock()
	user, hash = a.cfg.AdminUser, a.adminHash
	if a.auth.User != "" {
		user = a.auth.User
	}
	if a.auth.Hash != "" {
		hash, _ = parseHash(a.auth.Hash) // checked in loadAuth and on save
	}
	return user, hash, a.auth.TOTPSecret
}

func (a *App) totpOn() bool {
	a.authMu.RLock()
	defer a.authMu.RUnlock()
	return a.auth.TOTPSecret != ""
}

func (a *App) epoch() int64 {
	a.authMu.RLock()
	defer a.authMu.RUnlock()
	return a.auth.Epoch
}

// verifyLogin checks the three fields. The password is hashed even when the
// user name is wrong, so a wrong name takes as long as a wrong password.
func (a *App) verifyLogin(user, password, code string) bool {
	wantUser, hash, secret := a.credentials()
	userOK := subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(user))), []byte(strings.ToLower(wantUser))) == 1
	pwOK := len(password) <= maxPasswordLen && hash.verify(password)
	if !userOK || !pwOK {
		return false
	}
	if secret == "" {
		return true
	}
	return a.useCode(code)
}

// useCode accepts a code for the TOTP secret in force once.
func (a *App) useCode(code string) bool {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if a.auth.TOTPSecret == "" {
		return true
	}
	step := checkTOTP(a.auth.TOTPSecret, code, a.now(), a.auth.TOTPLast)
	if step == 0 {
		return false
	}
	a.auth.TOTPLast = step
	if err := a.saveAuthLocked(); err != nil {
		log.Printf("auth: saving the last code step: %v", err)
	}
	return true
}

// changeAuth applies fn to the account, raises the epoch (signing every
// browser out), saves, and gives this browser a fresh session.
func (a *App) changeAuth(w http.ResponseWriter, r *http.Request, fn func(s *authState)) error {
	a.authMu.Lock()
	old := a.auth
	fn(&a.auth)
	a.auth.Epoch++
	if err := a.saveAuthLocked(); err != nil {
		a.auth = old
		a.authMu.Unlock()
		return err
	}
	a.authMu.Unlock()
	a.setSession(w, r)
	return nil
}

func (a *App) setSession(w http.ResponseWriter, r *http.Request) {
	exp := a.now().Add(sessionMaxAge)
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: a.signSession(exp), Path: "/", Expires: exp,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

// validUser allows letters, digits and . _ - @, up to 64 characters.
func validUser(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._-@", r) {
			return false
		}
	}
	return true
}

type securityPage struct {
	adminBase
	User      string
	TOTPOn    bool
	FromEnv   bool   // the password is still the one from .env
	Pending   string // a 2FA setup waiting for its first code: the secret
	PendingQR string // its otpauth:// URI
}

func (a *App) handleSecurity(w http.ResponseWriter, r *http.Request) {
	data := securityPage{adminBase: a.adminBase(r, t(detectLang(r), "security"))}
	user, _, secret := a.credentials()
	data.User, data.TOTPOn = user, secret != ""
	a.authMu.RLock()
	data.FromEnv = a.auth.Hash == ""
	if a.pending != "" && a.now().Sub(a.pendingAt) < pendingTTL && !data.TOTPOn {
		data.Pending = a.pending
	}
	a.authMu.RUnlock()
	if data.Pending != "" {
		host := r.Host
		if a.cfg.BaseURL != "" {
			host = strings.TrimPrefix(strings.TrimPrefix(a.cfg.BaseURL, "https://"), "http://")
		}
		data.PendingQR = otpauthURI(data.Pending, user, host)
	}
	if e := r.URL.Query().Get("e"); e != "" {
		if _, ok := messages[e]; ok {
			data.Flash, data.Error = "", t(data.Lang, e)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, http.StatusOK, "admin_security", data)
}

func securityRedirect(w http.ResponseWriter, r *http.Request, key string, failed bool) {
	p := "m"
	if failed {
		p = "e"
	}
	http.Redirect(w, r, "/admin/security?"+p+"="+key, http.StatusSeeOther)
}

// confirm checks the current password and, with 2FA on, a code: what every
// change on this page asks for, so a session left open is not enough to
// take the account over. Attempts count against the login limit.
func (a *App) confirm(r *http.Request) (ok bool, key string) {
	if !a.loginLimit.allow(a.clientIP(r)) {
		return false, "too_many"
	}
	_, hash, secret := a.credentials()
	pw := r.FormValue("current")
	if len(pw) > maxPasswordLen || !hash.verify(pw) {
		return false, "sec_bad_current"
	}
	if secret != "" && !a.useCode(r.FormValue("code")) {
		return false, "sec_bad_code"
	}
	return true, ""
}

func (a *App) handleAccount(w http.ResponseWriter, r *http.Request) {
	if ok, key := a.confirm(r); !ok {
		securityRedirect(w, r, key, true)
		return
	}
	user := strings.TrimSpace(r.FormValue("user"))
	pw, pw2 := r.FormValue("new"), r.FormValue("new2")
	switch {
	case !validUser(user):
		securityRedirect(w, r, "sec_bad_user", true)
		return
	case pw != pw2:
		securityRedirect(w, r, "sec_mismatch", true)
		return
	case pw != "" && (len([]rune(pw)) < minPasswordLen || len(pw) > maxPasswordLen):
		securityRedirect(w, r, "sec_short", true)
		return
	}
	hash := ""
	if pw != "" {
		h, err := hashPassword(pw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hash = h
	}
	err := a.changeAuth(w, r, func(s *authState) {
		s.User = user
		if hash != "" {
			s.Hash = hash
		} else if s.Hash == "" {
			// Keep the .env password, but from now on in auth.json, so a
			// later change of .env does not swap it behind the page's back.
			s.Hash = a.cfg.AdminHash
		}
	})
	if err != nil {
		log.Printf("auth: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	log.Printf("auth: account changed (user %q, new password: %v), other sessions signed out", user, hash != "")
	securityRedirect(w, r, "sec_saved", false)
}

// handleTOTPStart makes a secret to scan. It stays the same for a while, so
// reloading the page does not invalidate what the phone already saved.
func (a *App) handleTOTPStart(w http.ResponseWriter, r *http.Request) {
	a.authMu.Lock()
	if a.pending == "" || a.now().Sub(a.pendingAt) >= pendingTTL {
		a.pending, a.pendingAt = newTOTPSecret(), a.now()
	}
	a.authMu.Unlock()
	http.Redirect(w, r, "/admin/security#totp", http.StatusSeeOther)
}

// handleTOTPEnable turns 2FA on with the first code from the app, which
// proves the phone holds the same secret.
func (a *App) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	if !a.loginLimit.allow(a.clientIP(r)) {
		securityRedirect(w, r, "too_many", true)
		return
	}
	a.authMu.RLock()
	secret, at := a.pending, a.pendingAt
	a.authMu.RUnlock()
	if secret == "" || a.now().Sub(at) >= pendingTTL {
		securityRedirect(w, r, "sec_expired", true)
		return
	}
	step := checkTOTP(secret, r.FormValue("code"), a.now(), 0)
	if step == 0 {
		securityRedirect(w, r, "sec_bad_code", true)
		return
	}
	err := a.changeAuth(w, r, func(s *authState) {
		s.TOTPSecret, s.TOTPLast = secret, step
		if s.Hash == "" {
			s.Hash = a.cfg.AdminHash
		}
		a.pending = ""
	})
	if err != nil {
		log.Printf("auth: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	log.Printf("auth: two-factor on, other sessions signed out")
	securityRedirect(w, r, "sec_totp_on", false)
}

func (a *App) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	if ok, key := a.confirm(r); !ok {
		securityRedirect(w, r, key, true)
		return
	}
	if err := a.changeAuth(w, r, func(s *authState) { s.TOTPSecret, s.TOTPLast = "", 0 }); err != nil {
		log.Printf("auth: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	log.Printf("auth: two-factor off")
	securityRedirect(w, r, "sec_totp_off", false)
}

func (a *App) handleSignOutOthers(w http.ResponseWriter, r *http.Request) {
	if err := a.changeAuth(w, r, func(s *authState) {}); err != nil {
		log.Printf("auth: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	securityRedirect(w, r, "sec_others_out", false)
}
