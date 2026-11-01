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
- **Bluesky**: a box in the editor posts a link card for the post (cover,
  title, excerpt) once it is published, scheduled posts included. Signs in
  with an app password; a post is never sent twice.
- **Comments elsewhere**: under every post, links to the author's Bluesky,
  Mastodon and X profiles (set in the settings; empty ones left out) and to
  the RSS feed, with the real brand marks from Simple Icons, inline.
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
- **Sign-in** with a user name, a password and, optionally, a code from an
  authenticator app (TOTP, RFC 6238). Each code works once; changing any of
  it signs every other browser out.
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
| `ADMIN_PASSWORD_HASH` | Required: the first password. Made by `blog hashpw`. |
| `ADMIN_USER` | The first user name, `admin` by default. |
| `ADMIN_UI` | `off` leaves out `/admin/` (404) and the password, for a standby copy of another install. |
| `BASE_URL` | Public origin, e.g. `https://blog.example.com`: absolute links in RSS and link previews. |
| `HOST_PORT` | Host port, 8096 by default. |
| `TZ` | Time zone for publication dates, e.g. `America/Toronto`. |
| `TRUST_PROXY_HEADERS` | `true` behind Cloudflare: the login rate limit reads `CF-Connecting-IP`. |
| `BLUESKY_HANDLE` | Optional. The Bluesky account that announces new posts. |
| `BLUESKY_APP_PASSWORD` | Its app password (Settings → App passwords in Bluesky), never the account password. |
| `BLUESKY_SERVICE` | The account's server, `https://bsky.social` by default. |

### Backups

[`tools/backup.sh`](tools/backup.sh) archives the data volume together with
`docker-compose.yml` and `.env`, while the blog keeps running, and keeps the
ten newest archives. Run it from cron:

```
30 3 * * * /bin/bash /home/you/cron-tasks/backup.sh
```

Restore with the blog stopped:

```bash
docker compose stop blog
docker run --rm --user 0 -v blog_data:/data -v ~/blog-backups:/b:ro \
  --entrypoint sh blog:local -c 'cd /data && tar -xzf /b/<archive> --strip-components=1 data/'
docker compose up -d
```

### A standby copy

A second install can serve the same blog from another machine, ready to take
over. Give it `ADMIN_UI=off` and no `BLUESKY_*`: an admin panel there would
edit a copy that the next refresh overwrites, and two senders would announce
every post twice. Keep its data volume an exact copy of the primary's, for
instance by streaming `tar -czf - -C /data .` from a container on the
primary and unpacking it into the standby's volume with the blog stopped;
both are safe to read while the primary runs, as for backups. Switching is
then a matter of where the hostname points. On the day it takes over for
good, set `ADMIN_UI` back and add the Bluesky lines: the user name, password
and 2FA came over in `auth.json`.

### Signing in

`ADMIN_USER` and `ADMIN_PASSWORD_HASH` are the first account. The admin's
Security page then changes the user name and password and turns on
two-factor sign-in; that is kept in `auth.json` in the data volume and wins
over `.env`. Locked out, for instance with the phone lost:

```bash
docker exec blog rm /data/auth.json /data/session.key && docker restart blog
```

brings back the `.env` account without the second factor and signs every
browser out.

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
