# Run Ocelot Locally - Quick Setup

## Download & Extract

```bash
# Extract the archive
tar -xzf ocelot-complete.tar.gz
cd Ocelot
```

## Prerequisites

### For Tracker
- **Go 1.21+** - [Download](https://go.dev/dl/)

### For Admin Panel
- **PHP 8.0+** - [Download](https://www.php.net/downloads)
- Required extensions: PDO, PDO_Sqlite, curl, json, mbstring

### Install PHP (by platform)

**macOS:**
```bash
brew install php
```

**Ubuntu/Debian:**
```bash
sudo apt install php php-sqlite3 php-curl php-mbstring
```

**Windows:**
- Download from https://windows.php.net/download/
- Or use XAMPP: https://www.apachefriends.org/

## Quick Start

### Step 1: Start the Tracker

```bash
# If binary doesn't work, rebuild for your platform
go build -o ocelot-tracker main.go

# Start tracker
./ocelot-tracker
```

Expected output:
```
🐆 Ocelot BitTorrent Tracker (Go Edition)
Loaded sample data:
   • 1 user (passkey: 0123456789abcdef0123456789abcdef)
   • 1 torrent
Ocelot tracker listening on :34000
```

### Step 2: Start Admin Panel

Open a **new terminal**:

```bash
cd admin
php -S 127.0.0.1:8080
```

Expected output:
```
PHP Development Server started
```

### Step 3: Access Admin Panel

Open your browser to: **http://127.0.0.1:8080/login.php**

**Default login:**
- Username: `admin`
- Password: `changeme`

## Test the Tracker

```bash
# Test announce
curl "http://127.0.0.1:34000/0123456789abcdef0123456789abcdef/announce?info_hash=sampleinfohash12345&peer_id=12345678901234567890&port=6881&uploaded=0&downloaded=0&left=1000000&event=started&compact=1"

# Expected response (bencoded):
# d8:completei0e10:incompletei1e8:intervali1800e...
```

## Configuration

### Change Admin Password

Edit `admin/config.php`:

```php
define('ADMIN_PASS', password_hash('YourNewPassword', PASSWORD_BCRYPT));
```

### Change Tracker Password

Edit `main.go`:

```go
SitePassword:   "your_secure_password_here",
```

Then rebuild: `go build -o ocelot-tracker main.go`

## Troubleshooting

### "Port already in use"

**Tracker (34000):**
```bash
# Find and kill process
lsof -i :34000
kill -9 <PID>
```

**Admin (8080):**
```bash
lsof -i :8080
kill -9 <PID>

# Or use a different port
php -S 127.0.0.1:8081
```

### "Permission denied" (macOS/Linux)

```bash
chmod +x ocelot-tracker
```

### "Cannot find PDO_Sqlite" (PHP)

**macOS:**
```bash
brew reinstall php
```

**Ubuntu:**
```bash
sudo apt install php-sqlite3
```

### Rebuild for Your Platform

If the included binary doesn't work:

```bash
# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -o ocelot-tracker main.go

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o ocelot-tracker main.go

# Windows
GOOS=windows GOARCH=amd64 go build -o ocelot-tracker.exe main.go

# Linux
GOOS=linux GOARCH=amd64 go build -o ocelot-tracker main.go
```

## Files Included

```
ocelot-tracker           # Pre-built binary (Linux x86_64)
main.go                  # Tracker source code
go.mod, go.sum           # Go dependencies
tracker/                 # Tracker package
  ├── server.go          # HTTP server
  ├── announce.go        # BitTorrent protocol
  ├── types.go           # Data structures
  └── db_sqlite.go       # Database layer
admin/                   # PHP admin panel
  ├── config.php         # Configuration
  ├── login.php          # Authentication
  ├── index.php          # Dashboard
  ├── users.php          # User management
  ├── torrents.php       # Torrent management
  ├── peers.php          # Peer monitoring
  ├── stats.php          # Statistics
  └── includes/          # Header/footer
docs/
  ├── QUICKSTART.md      # Detailed guide
  ├── KNUTH_ANALYSIS.md  # Algorithm analysis
  └── ...
```

## Architecture

```
Browser ──► Admin Panel (PHP :8080) ──► SQLite Database
                                         ▲
                                         │
BitTorrent Client ──► Tracker (Go :34000)─┘
```

## Next Steps

1. ✅ Test announce with sample data
2. ✅ Login to admin panel
3. 📝 Add your own users (Users page)
4. 📝 Add your own torrents (Torrents page)
5. 📊 Monitor activity (Dashboard & Stats)

## Need Help?

- Check `QUICKSTART.md` for detailed documentation
- Review `admin/README.md` for admin panel features
- See tracker logs: Check terminal where `./ocelot-tracker` is running

## Security Notes for Production

⚠️ Before deploying to production:

1. Change admin password in `admin/config.php`
2. Change `SitePassword` in `main.go`
3. Use HTTPS (Let's Encrypt)
4. Restrict admin panel access by IP
5. Enable firewall rules
6. Use systemd/supervisor for process management

See `QUICKSTART.md` for production deployment guide.
