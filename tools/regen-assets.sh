#!/bin/sh
# Re-vendor the pinned front-end dependencies and print their SRI digests.
#
# Run from the repository root after changing a version in admin/config.php:
#     sh tools/regen-assets.sh
#
# Paste the printed digests into the ASSETS table in admin/config.php, then
# run `php admin/selftest.php` to confirm the vendored bytes and the pinned
# digests agree. 'cdn' mode refuses to load if they do not.
set -e
V_TAILWIND=3.4.16
V_ALPINE=3.14.9
V_D3=7.9.0
DEST=admin/assets/vendor
mkdir -p "$DEST"

fetch() {
    curl -fsS --max-time 60 -o "$2" "$1"
    printf '%-28s sha384-%s\n' "$(basename "$2")" \
        "$(openssl dgst -sha384 -binary "$2" | openssl base64 -A)"
}

fetch "https://cdn.tailwindcss.com/${V_TAILWIND}" \
      "${DEST}/tailwind-${V_TAILWIND}.js"
fetch "https://cdn.jsdelivr.net/npm/alpinejs@${V_ALPINE}/dist/cdn.min.js" \
      "${DEST}/alpine-${V_ALPINE}.js"
fetch "https://cdn.jsdelivr.net/npm/d3@${V_D3}/dist/d3.min.js" \
      "${DEST}/d3-${V_D3}.js"
