package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238), what an authenticator app shows: HMAC-SHA1 over the
// number of 30-second steps since 1970, cut to six digits.
const (
	totpStep   = 30
	totpDigits = 6
	// One step either side: ±30 s between the Pi's clock and the phone's.
	totpWindow = 1
)

var totpBase32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret makes a 160-bit secret, the size RFC 4226 recommends.
func newTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return totpBase32.EncodeToString(b)
}

// decodeTOTPSecret accepts a secret as apps show it: grouped, any case.
func decodeTOTPSecret(s string) ([]byte, error) {
	s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimRight(s, "=")))
	return totpBase32.DecodeString(s)
}

func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

func totpCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(at.Unix()/totpStep)), nil
}

// checkTOTP returns the step a code belongs to, or 0. A code for a step at
// or before lastStep was used already: refused, so a code seen over a
// shoulder does not work for the rest of its half minute.
func checkTOTP(secret, code string, at time.Time, lastStep int64) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0
	}
	now := at.Unix() / totpStep
	for d := int64(-totpWindow); d <= totpWindow; d++ {
		step := now + d
		if hmac.Equal([]byte(hotp(key, uint64(step))), []byte(code)) && step > lastStep {
			return step
		}
	}
	return 0
}

// otpauthURI is what the QR code holds; issuer and account only name the
// entry in the app.
func otpauthURI(secret, account, issuer string) string {
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}
