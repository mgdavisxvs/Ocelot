# 🐆 Ocelot for XAMPP on macOS

## Quick Install

### 1. Download Package

Download: **ocelot-xampp-macos.tar.gz** (6.5 MB)

### 2. Extract to XAMPP

```bash
# Extract the package
tar -xzf ocelot-xampp-macos.tar.gz

# Copy to XAMPP htdocs
sudo cp -r ocelot-xampp /Applications/XAMPP/xamppfiles/htdocs/ocelot

# Set permissions
sudo chown -R $(whoami) /Applications/XAMPP/xamppfiles/htdocs/ocelot
chmod +x /Applications/XAMPP/xamppfiles/htdocs/ocelot/START.command
```

### 3. Start XAMPP Apache

Open XAMPP Control Panel:
```bash
open /Applications/XAMPP/manager-osx.app
```

Click **Start** next to Apache.

### 4. Launch Tracker

**Double-click:**
```
/Applications/XAMPP/xamppfiles/htdocs/ocelot/START.command
```

Or via Terminal:
```bash
cd /Applications/XAMPP/xamppfiles/htdocs/ocelot
./START.command
```

### 5. Access Admin Panel

Open browser to:

**http://localhost/ocelot/admin/login.php**

**Login:**
- Username: `admin`
- Password: `changeme`

## What's Included

- ✅ **Both macOS binaries** (Intel + Apple Silicon)
- ✅ **Auto-detection** (START.command picks correct binary)
- ✅ **PHP Admin Panel** (works with XAMPP's Apache)
- ✅ **Sample data** (1 user, 1 torrent for testing)
- ✅ **Documentation** (XAMPP_SETUP.md)

## URLs

| Service | URL |
|---------|-----|
| **Admin Panel** | http://localhost/ocelot/admin/ |
| **Dashboard** | http://localhost/ocelot/admin/index.php |
| **Tracker** | http://localhost:34000 |

## Test It

```bash
# Test tracker announce
curl "http://localhost:34000/0123456789abcdef0123456789abcdef/announce?info_hash=sampleinfohash12345&peer_id=12345678901234567890&port=6881&uploaded=0&downloaded=0&left=1000000&event=started&compact=1"

# Should return bencoded response:
# d8:completei0e10:incompletei1e8:intervali1800e...
```

## Troubleshooting

### macOS blocks the binary

```bash
cd /Applications/XAMPP/xamppfiles/htdocs/ocelot
xattr -d com.apple.quarantine ocelot-tracker-*
```

### Port 34000 already in use

```bash
lsof -ti:34000 | xargs kill -9
```

### Admin panel can't find database

Edit `/Applications/XAMPP/xamppfiles/htdocs/ocelot/admin/config.php`:

```php
define('DB_PATH', '/Applications/XAMPP/xamppfiles/htdocs/ocelot/data/db');
```

## File Locations

```
/Applications/XAMPP/xamppfiles/htdocs/ocelot/
├── START.command              # ← Double-click to start tracker
├── ocelot-tracker-arm64       # Apple Silicon binary
├── ocelot-tracker-intel       # Intel Mac binary
├── admin/                     # Admin panel (PHP)
│   ├── login.php             # ← http://localhost/ocelot/admin/login.php
│   ├── index.php             # Dashboard
│   ├── users.php             # User management
│   ├── torrents.php          # Torrent management
│   └── stats.php             # Statistics
├── data/db/                   # SQLite databases (auto-created)
└── XAMPP_SETUP.md            # Full documentation
```

## Security

⚠️ **Before production use:**

1. Change admin password in `admin/config.php`
2. Change tracker password in `main.go` (requires rebuild)
3. Enable XAMPP authentication
4. Use HTTPS (see XAMPP_SETUP.md)

## Support

See included documentation:
- **XAMPP_SETUP.md** - Complete XAMPP setup guide
- **QUICKSTART.md** - General deployment guide
- **admin/README.md** - Admin panel features

---

**Ready to use with XAMPP on macOS!**
