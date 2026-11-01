#!/bin/bash
# Backup of a go-blog install, for cron (e.g. 30 3 * * * /bin/bash backup.sh).
#
# One archive holds everything a restore needs:
#   data/     the data volume: blog.json (posts, settings, image records),
#             auth.json (user name, password hash, 2FA secret), session.key
#             and images/
#   project/  docker-compose.yml and .env from the compose directory
#
# Taken while the blog stays up: blog.json and auth.json are only ever
# replaced by an atomic rename, so tar reads either the old or the new file,
# never a half-written one.
#
# .env and auth.json hold secrets, so the backup directory is kept at 700.
#
# Restore, with the blog stopped (docker compose stop blog):
#   docker run --rm --user 0 -v blog_data:/data -v "$BACKUP_DIR:/b:ro" \
#     --entrypoint sh blog:local -c 'cd /data && tar -xzf /b/<archive> --strip-components=1 data/'
set -euo pipefail

BLOG_DIR="${BLOG_DIR:-$HOME/blog}"            # docker-compose.yml and .env
BACKUP_DIR="${BACKUP_DIR:-$HOME/blog-backups}"
VOLUME="${VOLUME:-blog_data}"
IMAGE="${IMAGE:-blog:local}"
KEEP="${KEEP:-10}"                             # newest archives kept
LOG="${LOG:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/blog-backup.log}"
DOCKER="${DOCKER:-/usr/bin/docker}"

exec >> "$LOG" 2>&1
echo "=== $(date "+%Y-%m-%d %H:%M:%S") start ==="

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

NAME="blog_backup_$(date +%Y-%m-%d_%H-%M-%S).tar.gz"
TMP="$BACKUP_DIR/.$NAME.part"
trap 'rm -f "$TMP"' EXIT

# The image runs as uid 1000, the owner of the volume's files. Run from
# root's crontab, .env is root's too, so tar reads as root.
AS_ROOT=()
[ "$(id -u)" -eq 0 ] && AS_ROOT=(--user 0)
$DOCKER run --rm --network none "${AS_ROOT[@]}" --entrypoint tar \
  -v "$VOLUME:/backup/data:ro" \
  -v "$BLOG_DIR/docker-compose.yml:/backup/project/docker-compose.yml:ro" \
  -v "$BLOG_DIR/.env:/backup/project/.env:ro" \
  "$IMAGE" -czf - -C /backup data project > "$TMP"

# Refuse an archive that is broken or lacks the database. grep reads to the
# end: with -q it would exit early, tar would die of SIGPIPE, and pipefail
# would call a good archive bad.
tar -tzf "$TMP" | grep -x "data/blog.json" >/dev/null || { echo "!! data/blog.json missing from the archive"; exit 1; }

mv "$TMP" "$BACKUP_DIR/$NAME"
echo "wrote $NAME ($(du -h "$BACKUP_DIR/$NAME" | cut -f1))"

cd "$BACKUP_DIR"
ls -1t blog_backup_*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm --

echo "=== $(date "+%Y-%m-%d %H:%M:%S") end ==="
