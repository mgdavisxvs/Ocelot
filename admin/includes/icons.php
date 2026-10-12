<?php
/**
 * Ocelot Admin - self-contained icon set.
 *
 * Replaces the `lucide@latest` runtime dependency (355 KB of JavaScript,
 * unpinned floating tag, no SRI) with ~5 KB of inline markup rendered
 * server-side. Removes one third-party origin and one script-execution
 * surface from every admin page.
 *
 * Icon geometry derived from Lucide (ISC licence), pinned at the exact
 * version below. Regenerate with tools/regen-icons.sh after changing it.
 *
 * LUCIDE_VERSION: 0.454.0
 */

/** Inner SVG geometry, keyed by icon name. Whitelist - nothing else renders. */
const OCELOT_ICONS = [
    'activity' => '<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2" />',
    'alert-circle' => '<circle cx="12" cy="12" r="10" /> <line x1="12" x2="12" y1="8" y2="12" /> <line x1="12" x2="12.01" y1="16" y2="16" />',
    'arrow-down' => '<path d="M12 5v14" /> <path d="m19 12-7 7-7-7" />',
    'arrow-up' => '<path d="m5 12 7-7 7 7" /> <path d="M12 19V5" />',
    'check-circle' => '<circle cx="12" cy="12" r="10" /> <path d="m9 12 2 2 4-4" />',
    'database' => '<ellipse cx="12" cy="5" rx="9" ry="3" /> <path d="M3 5V19A9 3 0 0 0 21 19V5" /> <path d="M3 12A9 3 0 0 0 21 12" />',
    'disc' => '<circle cx="12" cy="12" r="10" /> <circle cx="12" cy="12" r="2" />',
    'filter' => '<polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3" />',
    'info' => '<circle cx="12" cy="12" r="10" /> <path d="M12 16v-4" /> <path d="M12 8h.01" />',
    'log-out' => '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /> <polyline points="16 17 21 12 16 7" /> <line x1="21" x2="9" y1="12" y2="12" />',
    'menu' => '<line x1="4" x2="20" y1="12" y2="12" /> <line x1="4" x2="20" y1="6" y2="6" /> <line x1="4" x2="20" y1="18" y2="18" />',
    'plus' => '<path d="M5 12h14" /> <path d="M12 5v14" />',
    'refresh-cw' => '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" /> <path d="M21 3v5h-5" /> <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" /> <path d="M8 16H3v5" />',
    'server' => '<rect width="20" height="8" x="2" y="2" rx="2" ry="2" /> <rect width="20" height="8" x="2" y="14" rx="2" ry="2" /> <line x1="6" x2="6.01" y1="6" y2="6" /> <line x1="6" x2="6.01" y1="18" y2="18" />',
    'trash-2' => '<path d="M3 6h18" /> <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" /> <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" /> <line x1="10" x2="10" y1="11" y2="17" /> <line x1="14" x2="14" y1="11" y2="17" />',
    'trending-up' => '<polyline points="22 7 13.5 15.5 8.5 10.5 2 17" /> <polyline points="16 7 22 7 22 13" />',
    'upload' => '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /> <polyline points="17 8 12 3 7 8" /> <line x1="12" x2="12" y1="3" y2="15" />',
    'user-plus' => '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" /> <circle cx="9" cy="7" r="4" /> <line x1="19" x2="19" y1="8" y2="14" /> <line x1="22" x2="16" y1="11" y2="11" />',
    'users' => '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" /> <circle cx="9" cy="7" r="4" /> <path d="M22 21v-2a4 4 0 0 0-3-3.87" /> <path d="M16 3.13a4 4 0 0 1 0 7.75" />',
    'wifi-off' => '<path d="M12 20h.01" /> <path d="M8.5 16.429a5 5 0 0 1 7 0" /> <path d="M5 12.859a10 10 0 0 1 5.17-2.69" /> <path d="M19 12.859a10 10 0 0 0-2.007-1.523" /> <path d="M2 8.82a15 15 0 0 1 4.177-2.643" /> <path d="M22 8.82a15 15 0 0 0-11.288-3.764" /> <path d="m2 2 20 20" />',
    'x' => '<path d="M18 6 6 18" /> <path d="m6 6 12 12" />',
];

/** Aliases for icons Lucide renamed after 0.4x. Keeps existing markup valid. */
const OCELOT_ICON_ALIASES = [
    'circle-alert' => 'alert-circle',
    'circle-check' => 'check-circle',
];

/**
 * Render an icon as inline SVG.
 *
 * The name is resolved against a whitelist and is never interpolated into
 * output. An unknown name renders nothing rather than emitting attacker- or
 * caller-controlled text, so a dynamic $name can never become an injection
 * vector (cf. peers.php, which passes a computed name).
 *
 * @param string $name  icon key, e.g. 'database'
 * @param string $class CSS classes applied to the <svg> element
 */
function icon(string $name, string $class = 'w-5 h-5'): string
{
    $name = OCELOT_ICON_ALIASES[$name] ?? $name;
    $body = OCELOT_ICONS[$name] ?? null;
    if ($body === null) {
        return '';
    }
    return '<svg xmlns="http://www.w3.org/2000/svg" class="' . htmlspecialchars($class, ENT_QUOTES)
         . '" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"'
         . ' stroke-width="2" stroke-linecap="round" stroke-linejoin="round"'
         . ' aria-hidden="true" focusable="false">' . $body . '</svg>';
}

/** True if $name is a renderable icon. Used by the self-test. */
function icon_exists(string $name): bool
{
    $name = OCELOT_ICON_ALIASES[$name] ?? $name;
    return isset(OCELOT_ICONS[$name]);
}
