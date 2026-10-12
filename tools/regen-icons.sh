#!/bin/sh
# Regenerate admin/includes/icons.php from upstream Lucide SVG sources.
#
# Only the icons actually referenced by the admin panel are embedded. Add a
# name here when you introduce a new `icon('...')` call; `php admin/selftest.php`
# fails if code references an icon the whitelist does not carry.
#
# Two icons were renamed upstream after 0.4x and are fetched under their new
# names but registered under the old ones, so existing markup keeps working:
#     alert-circle -> circle-alert        check-circle -> circle-check
set -e
LUCIDE=0.454.0
ICONS="activity arrow-down arrow-up database disc filter info log-out menu plus
       refresh-cw server trash-2 trending-up upload user-plus users wifi-off x"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

for i in $ICONS; do
    curl -fsS --max-time 30 -o "$TMP/$i.svg" \
        "https://cdn.jsdelivr.net/npm/lucide-static@${LUCIDE}/icons/$i.svg"
done
curl -fsS --max-time 30 -o "$TMP/alert-circle.svg" \
    "https://cdn.jsdelivr.net/npm/lucide-static@${LUCIDE}/icons/circle-alert.svg"
curl -fsS --max-time 30 -o "$TMP/check-circle.svg" \
    "https://cdn.jsdelivr.net/npm/lucide-static@${LUCIDE}/icons/circle-check.svg"

echo "Fetched $(ls "$TMP" | wc -l) icons from lucide-static@${LUCIDE} into $TMP"
echo "Rebuild admin/includes/icons.php from these, preserving the icon()"
echo "whitelist semantics, then run: php admin/selftest.php"
