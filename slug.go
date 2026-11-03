package main

import (
	"strings"
	"unicode"
)

// translit spells Russian and Ukrainian letters in Latin for URL slugs.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'ґ': "g", 'д': "d", 'е': "e", 'ё': "e", 'є': "ye",
	'ж': "zh", 'з': "z", 'и': "i", 'і': "i", 'ї': "yi", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh",
	'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e",
	'ю': "yu", 'я': "ya",
}

// slugify makes a URL slug: lower-case Latin letters, digits and dashes.
// Cyrillic is transliterated, other letters and accents are dropped.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case translit[r] != "" || r == 'ъ' || r == 'ь':
			b.WriteString(translit[r])
			dash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// other scripts: skip
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

// tagSlug is the URL form of a tag name: /tag/{slug}/.
func tagSlug(name string) string {
	if s := slugify(name); s != "" {
		return s
	}
	return "tag"
}

// headingID makes an anchor for a heading. Unlike slugify it keeps letters
// of any script, so Russian headings get readable anchors.
func headingID(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "section"
}

// reservedSlugs are paths the blog itself uses at the top level.
var reservedSlugs = map[string]bool{
	"admin": true, "tag": true, "page": true, "rss": true, "img": true, "static": true,
	"healthz": true, "lang": true, "robots-txt": true, "sitemap-xml": true, "favicon-ico": true, "content": true,
}
