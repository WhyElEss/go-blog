# go-blog

A small, fast, self-hosted blog: one Go binary, posts written in Markdown,
images resized on upload, an RSS feed and a simple admin panel. Built to
replace Ghost on a Raspberry Pi, where it uses about 12 MB of RAM and
renders a page in a few milliseconds.

## Features

- **Markdown editor** with a live preview rendered by the server, a toolbar,
  ⌘S to save, and a warning about unsaved changes.
- **Images**: upload from the toolbar, paste from the clipboard or drop onto
  the text. Every upload is decoded, turned upright from its EXIF
  orientation, scaled to at most 2000 px wide, saved with 600/1000/1400 px
  copies for `srcset`, and stripped of metadata (camera, GPS). JPEG, PNG
  (transparency kept), GIF (kept as is, animation included), WebP (saved as
  JPEG) and SVG.
- **Image placement** next to the text: left, right or centre, with an
  optional caption. Floats turn into full-width images on phones.
- **YouTube**: a link on its own line becomes a privacy-enhanced player.
- **Posts and pages**: drafts, scheduled publication, tags with their own
  pages, standalone pages (About, Contact) outside the feed.
- **Two languages, one URL**: a post can have both an English and a Russian
  version. Readers get the one their browser asks for, can switch with the
  RU/EN buttons, and see the original when there is no translation.
- **RSS** with the full text and absolute links: `/rss/` in the default
  language, `/rss/en/` and `/rss/ru/`.
- **Light and dark** themes following the reader's system, with an optional
  separate logo for dark mode.
- **Admin panel** in English or Russian: posts, image library with usage
  counts, settings (title, description, menus, logo, icon).
- **Ghost migration**: a converter for Ghost's JSON export and an importer
  that runs the images through the same pipeline. Old `/content/images/…`
  URLs redirect to the new copies.

## Markdown

CommonMark with GitHub tables, strikethrough, task lists and bare-URL links,
plus:

```markdown
![Alt text](/img/2026/10/turntable.jpg#right "Caption under the image")

This paragraph flows around the image on the right.

![](/img/2026/10/band.jpg#left)

This one flows around the image on the left. Without a fragment, or with
#center, an image spans the text column.

https://www.youtube.com/watch?v=gYc5zrXC52k

## A heading with its own anchor {#anchor}
```

An image becomes a figure only when it is alone in its paragraph. Raw HTML
passes through: only the admin writes posts.

## English and Russian

Every post is written in its original language and may have a translation
into the other one; the editor has a tab for each. Cover, tags, date and URL
are shared. A page is shown in:

1. the language in `?lang=en` or `?lang=ru`, which links to one version;
2. else the reader's choice from the RU/EN switch (a cookie);
3. else the first of Russian and English in the browser's `Accept-Language`;
4. else the default language from the settings.

A post without that version is shown in its original language with a note
saying so. The blog's own texts (menus, dates, buttons) follow the same
choice; menu labels and the description take a translation in the settings
(`Label | Translated label | /url/`). Pages are sent with `Cache-Control:
no-store` and `Vary: Accept-Language, Cookie`, so no cache serves one
reader's language to another.

Two existing posts that are translations of each other can be joined, with
the blog stopped. The second one's URL then redirects to the first one's
with `?lang=`:

```bash
docker compose stop blog
docker compose run --rm blog merge <slug> <translation-slug>
docker compose up -d
```

## Running it

The Raspberry Pi needs only Docker; the image is built from this repository.

```bash
mkdir ~/blog && cd ~/blog
curl -O https://raw.githubusercontent.com/WhyElEss/go-blog/main/docker-compose.yml
curl -o .env https://raw.githubusercontent.com/WhyElEss/go-blog/main/.env.example
docker compose build
docker compose run --rm -it blog hashpw   # paste the line into .env
docker compose up -d
```

Then open `http://<host>:8096/admin/`. Data lives in the `blog_data` volume:
`blog.json` (posts, settings, image records), `session.key` and `images/`.

To update: `docker compose build --no-cache && docker compose up -d`.

### Configuration (`.env`)

| Variable | Meaning |
|---|---|
| `ADMIN_PASSWORD_HASH` | Required. Made by `blog hashpw`. |
| `BASE_URL` | Public origin, e.g. `https://blog.example.com`: absolute links in RSS and link previews. |
| `HOST_PORT` | Host port, 8096 by default. |
| `TZ` | Time zone for publication dates, e.g. `America/Toronto`. |
| `TRUST_PROXY_HEADERS` | `true` behind Cloudflare: the login rate limit reads `CF-Connecting-IP`. |

## Moving from Ghost

1. In Ghost, Settings → Labs → Export your content (JSON).
2. Convert it: `python3 tools/ghost2md.py ghost-export.json > import.json`.
3. Copy Ghost's `content/images` folder next to it and import into the
   blog's data volume, with the blog stopped:

```bash
docker compose stop blog
docker compose run --rm -v "$PWD/import.json:/import.json:ro" \
  -v "$PWD/ghost-images:/ghost-images:ro" blog import /import.json /ghost-images
docker compose up -d
```

Posts keep their slugs, so `/{slug}/` links keep working. Posts whose slug
already exists are skipped, so the import can be run again.

## Development

Go 1.26. `go test ./...` covers the Markdown dialect, the image pipeline
(resizing, EXIF orientation, SVG), the admin flow and the feed. CI runs
gofmt, vet, the tests with the race detector and an arm64 build.
