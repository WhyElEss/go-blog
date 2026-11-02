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
	"discuss":    {"Обсудить запись можно в", "Comments are welcome on"},
	"and":        {"и", "and"},
	"subscribe":  {"Подписаться на блог:", "Follow the blog:"},

	// admin
	"admin":            {"Админка", "Admin"},
	"posts":            {"Записи", "Posts"},
	"pages":            {"Страницы", "Pages"},
	"images":           {"Картинки", "Images"},
	"settings":         {"Настройки", "Settings"},
	"view_site":        {"Сайт", "View site"},
	"logout":           {"Выйти", "Sign out"},
	"login":            {"Вход", "Sign in"},
	"password":         {"Пароль", "Password"},
	"sign_in":          {"Войти", "Sign in"},
	"bad_password":     {"Неверный логин, пароль или код.", "Wrong user name, password or code."},
	"user":             {"Логин", "User name"},
	"code":             {"Код из приложения", "Code from the app"},
	"security":         {"Безопасность", "Security"},
	"sec_account":      {"Логин и пароль", "User name and password"},
	"sec_from_env":     {"Сейчас действует пароль из .env. Задайте здесь новый, свой для блога.", "The password in force is the one from .env. Set a new one, just for the blog."},
	"sec_new":          {"Новый пароль", "New password"},
	"sec_new2":         {"Новый пароль ещё раз", "New password again"},
	"sec_new_hint":     {"Не короче 12 символов. Пусто — пароль не меняется.", "At least 12 characters. Empty: the password stays."},
	"sec_current":      {"Текущий пароль", "Current password"},
	"sec_confirm":      {"Для подтверждения:", "To confirm:"},
	"sec_save":         {"Сохранить", "Save"},
	"sec_totp":         {"Двухфакторная аутентификация", "Two-factor authentication"},
	"sec_totp_is_on":   {"Включена: при входе нужен код из приложения-аутентификатора.", "On: signing in takes a code from the authenticator app."},
	"sec_totp_is_off":  {"Выключена: для входа достаточно логина и пароля.", "Off: a user name and password are enough to sign in."},
	"sec_totp_start":   {"Включить", "Turn on"},
	"sec_totp_scan":    {"Отсканируйте QR-код приложением-аутентификатором (1Password, «Пароли» на iPhone, Google Authenticator) или введите ключ вручную, затем введите код, который оно покажет.", "Scan the QR code with an authenticator app (1Password, Passwords on iPhone, Google Authenticator) or type the key in, then enter the code it shows."},
	"sec_totp_key":     {"Ключ", "Key"},
	"sec_totp_enable":  {"Подтвердить и включить", "Confirm and turn on"},
	"sec_totp_off_btn": {"Выключить", "Turn off"},
	"sec_others":       {"Другие устройства", "Other devices"},
	"sec_others_hint":  {"Вход действует 30 дней. Смена логина, пароля или 2FA выводит из всех браузеров, кроме этого.", "A sign-in lasts 30 days. Changing the user name, password or 2FA signs out every browser but this one."},
	"sec_others_btn":   {"Выйти на всех остальных устройствах", "Sign out everywhere else"},
	"sec_recover":      {"Если потеряете телефон: на Pi выполните docker exec blog rm /data/auth.json /data/session.key && docker restart blog — вернутся логин и пароль из .env, без 2FA.", "Lost the phone? On the Pi run docker exec blog rm /data/auth.json /data/session.key && docker restart blog: the user name and password from .env come back, without 2FA."},
	"sec_saved":        {"Сохранено. Остальные устройства вышли.", "Saved. Other devices are signed out."},
	"sec_totp_on":      {"Двухфакторная аутентификация включена. Остальные устройства вышли.", "Two-factor authentication is on. Other devices are signed out."},
	"sec_totp_off":     {"Двухфакторная аутентификация выключена.", "Two-factor authentication is off."},
	"sec_others_out":   {"Все остальные устройства вышли.", "Every other device is signed out."},
	"sec_bad_current":  {"Неверный текущий пароль.", "Wrong current password."},
	"sec_bad_code":     {"Код не подошёл. Если он верный, проверьте время на телефоне.", "The code did not match. If it is right, check the phone's clock."},
	"sec_bad_user":     {"Логин: буквы, цифры и . _ - @, до 64 символов.", "User name: letters, digits and . _ - @, up to 64 characters."},
	"sec_mismatch":     {"Новые пароли не совпадают.", "The new passwords differ."},
	"sec_short":        {"Пароль должен быть не короче 12 символов.", "The password needs at least 12 characters."},
	"sec_expired":      {"Настройка устарела, начните заново.", "The setup expired: start again."},
	"too_many":         {"Слишком много попыток. Подождите 15 минут.", "Too many attempts. Wait 15 minutes."},
	"new_post":         {"Новая запись", "New post"},
	"new_page":         {"Новая страница", "New page"},
	"edit_post":        {"Редактирование", "Edit"},
	"title":            {"Заголовок", "Title"},
	"slug":             {"Адрес", "URL"},
	"slug_hint":        {"Пусто — из заголовка.", "Empty: made from the title."},
	"body":             {"Текст (Markdown)", "Text (Markdown)"},
	"preview":          {"Предпросмотр", "Preview"},
	"excerpt":          {"Анонс", "Excerpt"},
	"excerpt_hint":     {"Для списка записей и превью ссылок. Пусто — начало текста.", "For the post list and link previews. Empty: the start of the text."},
	"cover":            {"Обложка", "Cover image"},
	"upload":           {"Загрузить", "Upload"},
	"remove":           {"Убрать", "Remove"},
	"tags":             {"Теги", "Tags"},
	"tags_hint":        {"Через запятую.", "Comma-separated."},
	"lang":             {"Язык оригинала", "Original language"},
	"lang_hint":        {"Его видят читатели, для чьего языка нет перевода.", "Shown to readers whose language has no translation."},
	"tab_empty":        {"Перевода нет: заполните заголовок и текст, чтобы он появился.", "No translation: fill in the title and text to add one."},
	"err_title_tr":     {"У перевода нужен заголовок.", "The translation needs a title."},
	"published":        {"Дата публикации", "Publication date"},
	"published_hint":   {"Пусто — сейчас. Дата в будущем — запись появится в это время.", "Empty: now. A future date publishes the post at that time."},
	"status":           {"Статус", "Status"},
	"st_draft":         {"Черновик", "Draft"},
	"st_published":     {"Опубликовано", "Published"},
	"st_scheduled":     {"Запланировано", "Scheduled"},
	"is_page":          {"Отдельная страница (не в ленте и не в RSS)", "Standalone page (not in the list or RSS)"},
	"save":             {"Сохранить", "Save"},
	"publish":          {"Опубликовать", "Publish"},
	"unpublish":        {"В черновики", "Unpublish"},
	"delete":           {"Удалить", "Delete"},
	"confirm_delete":   {"Удалить запись насовсем?", "Delete this post for good?"},
	"open":             {"Открыть", "Open"},
	"saved":            {"Сохранено.", "Saved."},
	"deleted":          {"Удалено.", "Deleted."},
	"err_title":        {"Нужен заголовок.", "A title is required."},
	"err_slug":         {"Этот адрес уже занят или зарезервирован.", "This URL is taken or reserved."},
	"err_date":         {"Не понял дату.", "Could not read the date."},
	"unsaved":          {"Есть несохранённые изменения.", "There are unsaved changes."},
	"img_left":         {"Картинка слева", "Image left"},
	"img_center":       {"Картинка по центру", "Image centre"},
	"img_right":        {"Картинка справа", "Image right"},
	"uploading":        {"Загружаю…", "Uploading…"},
	"upload_failed":    {"Не удалось загрузить: ", "Upload failed: "},
	"tb_bold":          {"Жирный", "Bold"},
	"tb_italic":        {"Курсив", "Italic"},
	"tb_strike":        {"Зачёркнутый", "Strikethrough"},
	"tb_mark":          {"Выделить цветом: ==текст==", "Highlight: ==text=="},
	"tb_code":          {"Код в строке", "Inline code"},
	"tb_link":          {"Ссылка", "Link"},
	"tb_h2":            {"Заголовок 2", "Heading 2"},
	"tb_h3":            {"Заголовок 3", "Heading 3"},
	"tb_h4":            {"Заголовок 4", "Heading 4"},
	"tb_list":          {"Список", "Bulleted list"},
	"tb_olist":         {"Нумерованный список", "Numbered list"},
	"tb_task":          {"Список задач", "Task list"},
	"tb_quote":         {"Цитата", "Quote"},
	"tb_codeblock":     {"Блок кода", "Code block"},
	"tb_table":         {"Таблица", "Table"},
	"tb_hr":            {"Разделитель", "Divider"},
	"tb_footnote":      {"Сноска", "Footnote"},
	"tb_toc":           {"Оглавление: [TOC] — список заголовков ниже, со ссылками", "Contents: [TOC] lists the headings below it, linked"},
	"tb_toc_label":     {"Оглавление", "Contents"},
	"tb_image":         {"Картинка:", "Image:"},
	"tb_youtube":       {"Видео YouTube", "YouTube video"},
	"tb_url":           {"Адрес ссылки", "Link address"},
	"tb_yt_url":        {"Ссылка на видео YouTube", "YouTube video link"},
	"md_help":          {"Картинка: ![подпись](/img/…#left \"Подпись под ней\") — #left, #right или #center. Ссылка на YouTube отдельной строкой — плеер. ==текст== — выделение, [TOC] — оглавление, текст[^1] и строка [^1]: … — сноска, ## Заголовок {#якорь} — свой якорь. Картинки можно вставить из буфера или перетащить в текст.", "Image: ![alt](/img/…#left \"Caption\") with #left, #right or #center. A YouTube link on its own line becomes a player. ==text== highlights, [TOC] makes a table of contents, text[^1] with a line [^1]: … makes a footnote, ## Heading {#anchor} sets an anchor. Paste or drop images into the text."},
	"no_items":         {"Пока пусто.", "Nothing yet."},
	"col_title":        {"Заголовок", "Title"},
	"col_date":         {"Дата", "Date"},
	"col_status":       {"Статус", "Status"},
	"img_uses":         {"Используется: %d", "Used: %d"},
	"img_unused":       {"Не используется", "Unused"},
	"copy_md":          {"Копировать Markdown", "Copy Markdown"},
	"copied":           {"Скопировано", "Copied"},
	"confirm_img_del":  {"Удалить картинку со всеми копиями?", "Delete the image and all its copies?"},
	"img_in_use":       {"Картинка используется — сначала уберите её из записей.", "The image is in use: remove it from posts first."},
	"img_hint":         {"Загруженные картинки уменьшаются до 2000 px по ширине, плюс копии 600/1000/1400 px для телефонов. Метаданные (камера, GPS) удаляются.", "Uploads are scaled to at most 2000 px wide, plus 600/1000/1400 px copies for phones. Metadata (camera, GPS) is removed."},
	"site_title":       {"Название блога", "Blog title"},
	"site_desc":        {"Описание", "Description"},
	"site_desc_alt":    {"Описание на втором языке", "Description in the second language"},
	"site_lang":        {"Язык по умолчанию", "Default language"},
	"site_lang_hint":   {"Для читателей, чей браузер не просит ни русский, ни английский, и для ленты /rss/. Описание и названия в меню пишутся на нём, переводы — на втором языке.", "For readers whose browser asks for neither Russian nor English, and for the feed at /rss/. The description and menu labels are in this language, their translations in the other."},
	"nav":              {"Меню в шапке", "Header menu"},
	"footer_nav":       {"Ссылки в подвале", "Footer links"},
	"nav_hint":         {"По строке: Название | /адрес/ или Название | Перевод | /адрес/", "One per line: Label | /url/ or Label | Translated label | /url/"},
	"logo":             {"Логотип", "Logo"},
	"logo_dark":        {"Логотип для тёмной темы", "Logo for dark mode"},
	"icon":             {"Иконка сайта (квадратная)", "Site icon (square)"},
	"profiles":         {"Профили для обсуждения", "Profiles for comments"},
	"profiles_hint":    {"Ссылки под каждой записью, вместе с RSS; Mastodon ещё и в подвале, с rel=\"me\" для подтверждения в профиле. Пустое поле не показывается; нет ни одного — нет и блока.", "Linked under every post, together with RSS; Mastodon also in the footer, with rel=\"me\" for verification on the profile. Empty fields are left out; with none, there is no box."},
	"tags_manage":      {"Теги", "Tags"},
	"tags_manage_hint": {"Переименование и удаление меняют тег во всех записях, включая черновики. Если у нового имени другой адрес, старая страница /tag/…/ перестанет открываться — ссылки на неё в меню поправьте сами. Имя, которое уже есть у другого тега, объединит их.", "Renaming and deleting change the tag on every post, drafts included. If the new name has a different address, the old /tag/…/ page stops working: change menu links to it yourself. A name another tag already has merges the two."},
	"col_tag":          {"Тег", "Tag"},
	"col_posts":        {"Записей", "Posts"},
	"rename":           {"Переименовать", "Rename"},
	"confirm_tag_del":  {"Убрать этот тег из всех записей? Сами записи останутся.", "Remove this tag from every post? The posts stay."},
	"tag_renamed":      {"Тег переименован.", "Tag renamed."},
	"tag_deleted":      {"Тег удалён из записей.", "Tag removed from posts."},
	"tag_name_failed":  {"Имя тега не может быть пустым или содержать запятую.", "A tag name cannot be empty or contain a comma."},
	"per_page":         {"Записей на странице", "Posts per page"},
	"words":            {"слов", "words"},
	"bsky_send":        {"Отправить ссылку в Bluesky (оттуда Crossposter перешлёт её в X и Mastodon)", "Post the link to Bluesky (Crossposter forwards it to X and Mastodon)"},
	"bsky_later":       {"Уйдёт в момент публикации.", "Goes out when the post is published."},
	"bsky_now":         {"Уйдёт при сохранении.", "Goes out when you save."},
	"bsky_done":        {"Bluesky: отправлено %s.", "Bluesky: sent %s."},
	"bsky_open":        {"Открыть пост в Bluesky", "Open the Bluesky post"},
	"bsky_error":       {"Не удалось отправить: %s. Сохраните запись с галочкой, чтобы попробовать ещё раз.", "Could not send: %s. Save with the box ticked to try again."},
	"bsky_off":         {"Bluesky не настроен: нужны BLUESKY_HANDLE и BLUESKY_APP_PASSWORD в .env.", "Bluesky is not set up: BLUESKY_HANDLE and BLUESKY_APP_PASSWORD in .env are needed."},
	"bsky_sent":        {"Сохранено и отправлено в Bluesky.", "Saved and posted to Bluesky."},
	"bsky_failed":      {"Сохранено, но в Bluesky отправить не удалось — подробности ниже.", "Saved, but posting to Bluesky failed: see below."},
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
