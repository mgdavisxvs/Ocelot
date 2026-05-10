# Ocelot Tracker Admin Panel

Modern PHP admin interface for the Ocelot BitTorrent tracker (Go edition).

## Features

- **Dashboard** - Real-time statistics, peer distribution charts, activity timelines
- **User Management** - Add/remove users, manage passkeys, view upload/download stats
- **Torrent Management** - Add torrents, monitor health, view seeder/leecher ratios
- **Peer Monitoring** - Real-time peer list with filtering and pagination
- **Statistics** - Comprehensive analytics with D3.js visualizations
- **SQLite Integration** - Direct access to tracker database shards

## Tech Stack

- **Backend**: PHP 8.0+ with PDO SQLite
- **Frontend**: Tailwind CSS (CDN)
- **JavaScript**: Alpine.js for reactivity
- **Charts**: D3.js v7
- **Icons**: Lucide Icons

## Requirements

- PHP 8.0 or higher
- PHP Extensions:
  - PDO
  - PDO_Sqlite
  - curl
  - json
  - mbstring
- Web server (Apache, Nginx, or PHP built-in)
- Ocelot tracker running on `localhost:34000`

## Installation

### 1. Configure Tracker Connection

Edit `config.php`:

```php
define('DB_PATH', '../data/db'); // Path to SQLite shards
define('TRACKER_URL', 'http://localhost:34000');
define('SITE_PASSWORD', 'changeme'); // Must match tracker config
```

### 2. Set Admin Credentials

```php
define('ADMIN_USER', 'admin');
define('ADMIN_PASS', password_hash('your_password', PASSWORD_BCRYPT));
```

### 3. Start PHP Server

```bash
cd admin
php -S 0.0.0.0:8080
```

Or configure Apache/Nginx to serve the `admin/` directory.

### 4. Access Admin Panel

Open http://localhost:8080/login.php

Default credentials:
- Username: `admin`
- Password: `changeme`

## File Structure

```
admin/
├── config.php              # Configuration and database helpers
├── login.php               # Authentication
├── logout.php              # Logout handler
├── index.php               # Dashboard with charts
├── users.php               # User management
├── torrents.php            # Torrent management
├── peers.php               # Active peer listing
├── stats.php               # Detailed statistics
├── includes/
│   ├── header.php          # Common header with navigation
│   └── footer.php          # Common footer
└── api/                    # AJAX endpoints (future)
```

## Database Access

The admin panel connects directly to Ocelot's SQLite shards:

- **Current Shard**: Most recent `ocelot_YYYYMMDD_HHMMSS.db`
- **Historical Shards**: All shards in `data/db/`
- **Query All**: Use `OcelotDB::queryAllShards()` for aggregate queries

### Example: Query Across All Shards

```php
$allPeers = OcelotDB::queryAllShards("
    SELECT user_id, SUM(uploaded) as total_uploaded
    FROM peers
    GROUP BY user_id
    ORDER BY total_uploaded DESC
    LIMIT 100
");
```

## Tracker API Integration

The admin panel communicates with the tracker via HTTP:

### Add User

```php
TrackerAPI::updateUser(
    userID: 123,
    passkey: '0123456789abcdef0123456789abcdef',
    canLeech: true,
    isProtected: false
);
```

### Add Torrent

```php
TrackerAPI::addTorrent(
    torrentID: 456,
    infoHash: 'a94a8fe5ccb19ba61c4c0873d391e987982fbbd3'
);
```

### Remove User

```php
TrackerAPI::deleteUser(userID: 123);
```

## Features Breakdown

### Dashboard (`index.php`)

- **Stat Cards**: Total peers, seeders, leechers, announces
- **Pie Chart**: Peer distribution (D3.js)
- **Timeline**: Activity over last hour
- **Tables**: Top uploaders, recent snatches

### User Management (`users.php`)

- View active users with upload/download stats
- Generate random 32-character passkeys
- Set leech privileges and IP protection
- Remove users from tracker

### Torrent Management (`torrents.php`)

- List active torrents with peer counts
- Health indicators (seeder ratio)
- Snatch statistics
- Add new torrents via info hash

### Peer Monitoring (`peers.php`)

- Real-time peer list (2-hour window)
- Filter by torrent ID, user ID, or peer type
- Pagination (50 peers per page)
- Upload/download speeds
- User agent and IP information

### Statistics (`stats.php`)

- 24-hour activity overview
- Hourly announce chart (D3.js bar chart)
- Database shard usage visualization
- Top torrents by activity
- System information

## Security Notes

⚠️ **Production Deployment:**

1. **Change default credentials** immediately
2. **Enable HTTPS** - Do not serve over plain HTTP
3. **Restrict access** - Use firewall rules or `.htaccess`
4. **Update SITE_PASSWORD** to match tracker
5. **Validate inputs** - The current implementation has basic validation
6. **Add rate limiting** - Prevent brute force attacks
7. **Use secure sessions** - Configure `session.cookie_secure` and `session.cookie_httponly`

### Recommended Apache Configuration

```apache
<Directory /var/www/ocelot/admin>
    # Require authentication
    AuthType Basic
    AuthName "Ocelot Admin"
    AuthUserFile /etc/apache2/.htpasswd
    Require valid-user

    # Restrict to specific IPs
    Require ip 192.168.1.0/24
</Directory>
```

## Customization

### Theme Colors

Edit in `includes/header.php`:

```javascript
tailwind.config = {
    theme: {
        extend: {
            colors: {
                primary: '#3b82f6',    // Blue
                secondary: '#8b5cf6'   // Purple
            }
        }
    }
}
```

### Pagination Limit

Edit in `config.php`:

```php
define('ITEMS_PER_PAGE', 50); // Default: 50
```

### Session Timeout

```php
define('SESSION_TIMEOUT', 3600); // 1 hour
```

## Troubleshooting

### "Database directory not found"

Ensure the tracker has created the database directory:

```bash
mkdir -p data/db
chmod 755 data/db
```

### "Tracker API error: HTTP 403"

Check that `SITE_PASSWORD` matches the tracker's configuration:

```bash
grep SitePassword ocelot.conf
```

### Charts not rendering

Ensure D3.js CDN is accessible:

```html
<script src="https://d3js.org/d3.v7.min.js"></script>
```

Check browser console for JavaScript errors.

### Icons not showing

Verify Lucide Icons CDN and initialization:

```html
<script src="https://unpkg.com/lucide@latest"></script>
<script>lucide.createIcons();</script>
```

## Development

### Adding New Pages

1. Create `newpage.php`
2. Include config and require auth:
   ```php
   <?php
   require_once 'config.php';
   requireAuth();
   $pageTitle = 'New Page';
   ```
3. Add navigation link in `includes/header.php`
4. Include footer: `<?php include 'includes/footer.php'; ?>`

### Adding API Endpoints

Create files in `api/` directory:

```php
<?php
require_once '../config.php';
requireAuth();

header('Content-Type: application/json');

// Your endpoint logic
echo json_encode(['success' => true]);
```

## Performance

- **Database Queries**: Automatically cached connections per shard
- **Pagination**: Limits memory usage for large datasets
- **CDN Resources**: Tailwind, Alpine, D3, Lucide loaded from CDN
- **Minimal PHP**: No heavy frameworks, just native PDO

## Contributing

This admin panel is part of the Ocelot Go port. Contributions welcome:

1. Fork the repository
2. Create a feature branch
3. Test thoroughly (especially SQL injection prevention)
4. Submit a pull request

## License

Same as Ocelot tracker (check main repository).

## Credits

- **Ocelot C++**: Original WhatCD tracker
- **Ocelot Go Port**: Modern rewrite with SQLite
- **Admin Panel**: Built with ❤️ using modern web technologies
