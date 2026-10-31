package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// messages holds every UI text as {Russian, English}. Both the public pages
// and the admin follow the reader: ?lang= on public pages, then the lang
// cookie, then Accept-Language, then the site's default (English for the
// admin).
var messages = map[string][2]string{
	// public
	"newer":      {"← Новее", "← Newer posts"},
	"older":      {"Раньше →", "Older posts →"},
	"tagged":     {"Записи с тегом", "Posts tagged"},
	"no_posts":   {"Здесь пока ничего нет.", "Nothing here yet."},
	"not_found":  {"Страница не найдена.", "Page not found."},
	"home":       {"На главную", "Home"},
	"rss":        {"RSS", "RSS"},
	"draft_note": {"Черновик: видите только вы.", "Draft: only you can see it."},
	"sched_note": {"Запланировано на %s: пока видите только вы.", "Scheduled for %s: only you can see it for now."},
	"edit":       {"Редактировать", "Edit"},
	"page_n":     {"страница %d", "page %d"},
	"only_ru":    {"Этот текст есть только на русском.", "This post is only available in Russian."},
	"only_en":    {"Этот текст есть только на английском.", "This post is only available in English."},
	// read_in is written in the language it offers.
	"read_in":    {"Читать на русском", "Read in English"},
	"lang_name":  {"Русский", "English"},
	"to_top":     {"Наверх", "Back to top"},
	"more_posts": {"Другие записи", "More posts"},
	"nav_older":  {"← Раньше", "← Older"},
	"nav_newer":  {"Позже →", "Newer →"},

	// admin
	"admin":           {"Админка", "Admin"},
	"posts":           {"Записи", "Posts"},
	"pages":           {"Страницы", "Pages"},
	"images":          {"Картинки", "Images"},
	"settings":        {"Настройки", "Settings"},
	"view_site":       {"Сайт", "View site"},
	"logout":          {"Выйти", "Sign out"},
	"login":           {"Вход", "Sign in"},
	"password":        {"Пароль", "Password"},
	"sign_in":         {"Войти", "Sign in"},
	"bad_password":    {"Неверный пароль.", "Wrong password."},
	"too_many":        {"Слишком много попыток. Подождите 15 минут.", "Too many attempts. Wait 15 minutes."},
	"new_post":        {"Новая запись", "New post"},
	"new_page":        {"Новая страница", "New page"},
	"edit_post":       {"Редактирование", "Edit"},
	"title":           {"Заголовок", "Title"},
	"slug":            {"Адрес", "URL"},
	"slug_hint":       {"Пусто — из заголовка.", "Empty: made from the title."},
	"body":            {"Текст (Markdown)", "Text (Markdown)"},
	"preview":         {"Предпросмотр", "Preview"},
	"excerpt":         {"Анонс", "Excerpt"},
	"excerpt_hint":    {"Для списка записей и превью ссылок. Пусто — начало текста.", "For the post list and link previews. Empty: the start of the text."},
	"cover":           {"Обложка", "Cover image"},
	"upload":          {"Загрузить", "Upload"},
	"remove":          {"Убрать", "Remove"},
	"tags":            {"Теги", "Tags"},
	"tags_hint":       {"Через запятую.", "Comma-separated."},
	"lang":            {"Язык оригинала", "Original language"},
	"lang_hint":       {"Его видят читатели, для чьего языка нет перевода.", "Shown to readers whose language has no translation."},
	"tab_empty":       {"Перевода нет: заполните заголовок и текст, чтобы он появился.", "No translation: fill in the title and text to add one."},
	"err_title_tr":    {"У перевода нужен заголовок.", "The translation needs a title."},
	"published":       {"Дата публикации", "Publication date"},
	"published_hint":  {"Пусто — сейчас. Дата в будущем — запись появится в это время.", "Empty: now. A future date publishes the post at that time."},
	"status":          {"Статус", "Status"},
	"st_draft":        {"Черновик", "Draft"},
	"st_published":    {"Опубликовано", "Published"},
	"st_scheduled":    {"Запланировано", "Scheduled"},
	"is_page":         {"Отдельная страница (не в ленте и не в RSS)", "Standalone page (not in the list or RSS)"},
	"save":            {"Сохранить", "Save"},
	"publish":         {"Опубликовать", "Publish"},
	"unpublish":       {"В черновики", "Unpublish"},
	"delete":          {"Удалить", "Delete"},
	"confirm_delete":  {"Удалить запись насовсем?", "Delete this post for good?"},
	"open":            {"Открыть", "Open"},
	"saved":           {"Сохранено.", "Saved."},
	"deleted":         {"Удалено.", "Deleted."},
	"err_title":       {"Нужен заголовок.", "A title is required."},
	"err_slug":        {"Этот адрес уже занят или зарезервирован.", "This URL is taken or reserved."},
	"err_date":        {"Не понял дату.", "Could not read the date."},
	"unsaved":         {"Есть несохранённые изменения.", "There are unsaved changes."},
	"img_left":        {"Картинка слева", "Image left"},
	"img_center":      {"Картинка по центру", "Image centre"},
	"img_right":       {"Картинка справа", "Image right"},
	"uploading":       {"Загружаю…", "Uploading…"},
	"upload_failed":   {"Не удалось загрузить: ", "Upload failed: "},
	"md_help":         {"Картинка: ![подпись](/img/…#left \"Подпись под ней\") — #left, #right или #center. Ссылка на YouTube отдельной строкой — плеер. Картинки можно вставить из буфера или перетащить в текст.", "Image: ![alt](/img/…#left \"Caption\") with #left, #right or #center. A YouTube link on its own line becomes a player. Paste or drop images into the text."},
	"no_items":        {"Пока пусто.", "Nothing yet."},
	"col_title":       {"Заголовок", "Title"},
	"col_date":        {"Дата", "Date"},
	"col_status":      {"Статус", "Status"},
	"img_uses":        {"Используется: %d", "Used: %d"},
	"img_unused":      {"Не используется", "Unused"},
	"copy_md":         {"Копировать Markdown", "Copy Markdown"},
	"copied":          {"Скопировано", "Copied"},
	"confirm_img_del": {"Удалить картинку со всеми копиями?", "Delete the image and all its copies?"},
	"img_in_use":      {"Картинка используется — сначала уберите её из записей.", "The image is in use: remove it from posts first."},
	"img_hint":        {"Загруженные картинки уменьшаются до 2000 px по ширине, плюс копии 600/1000/1400 px для телефонов. Метаданные (камера, GPS) удаляются.", "Uploads are scaled to at most 2000 px wide, plus 600/1000/1400 px copies for phones. Metadata (camera, GPS) is removed."},
	"site_title":      {"Название блога", "Blog title"},
	"site_desc":       {"Описание", "Description"},
	"site_desc_alt":   {"Описание на втором языке", "Description in the second language"},
	"site_lang":       {"Язык по умолчанию", "Default language"},
	"site_lang_hint":  {"Для читателей, чей браузер не просит ни русский, ни английский, и для ленты /rss/. Описание и названия в меню пишутся на нём, переводы — на втором языке.", "For readers whose browser asks for neither Russian nor English, and for the feed at /rss/. The description and menu labels are in this language, their translations in the other."},
	"nav":             {"Меню в шапке", "Header menu"},
	"footer_nav":      {"Ссылки в подвале", "Footer links"},
	"nav_hint":        {"По строке: Название | /адрес/ или Название | Перевод | /адрес/", "One per line: Label | /url/ or Label | Translated label | /url/"},
	"logo":            {"Логотип", "Logo"},
	"logo_dark":       {"Логотип для тёмной темы", "Logo for dark mode"},
	"icon":            {"Иконка сайта (квадратная)", "Site icon (square)"},
	"per_page":        {"Записей на странице", "Posts per page"},
	"words":           {"слов", "words"},
}

func t(lang, key string) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	if lang == "ru" {
		return m[0]
	}
	return m[1]
}

func tf(lang, key string, args ...any) string { return fmt.Sprintf(t(lang, key), args...) }

// detectLang picks the admin language.
func detectLang(r *http.Request) string { return prefLang(r, "en") }

// readerLang picks the language a public page is shown in: ?lang= (a link
// to one version), else the reader's choice and browser as for the admin,
// else the site's default.
func readerLang(r *http.Request, fallback string) string {
	if l := r.URL.Query().Get("lang"); l == "ru" || l == "en" {
		return l
	}
	return prefLang(r, normLang(fallback))
}

// prefLang reads the lang cookie set by the RU/EN switch, else the first
// of Russian and English in Accept-Language, else fallback.
func prefLang(r *http.Request, fallback string) string {
	if c, err := r.Cookie("lang"); err == nil && (c.Value == "ru" || c.Value == "en") {
		return c.Value
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "ru"):
			return "ru"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return fallback
}

var monthsRU = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}

// formatDate writes a date the way readers of that language expect.
func formatDate(lang string, d time.Time) string {
	if lang == "ru" {
		return fmt.Sprintf("%d %s %d", d.Day(), monthsRU[d.Month()-1], d.Year())
	}
	return d.Format("January 2, 2006")
}

func normLang(l string) string {
	if l == "ru" {
		return "ru"
	}
	return "en"
}

// otherLang is the blog's language that l is not.
func otherLang(l string) string {
	if normLang(l) == "ru" {
		return "en"
	}
	return "ru"
}

// pickText returns alt when lang is not the default language and alt is set.
func pickText(lang, defaultLang, main, alt string) string {
	if alt != "" && normLang(lang) != normLang(defaultLang) {
		return alt
	}
	return main
}
