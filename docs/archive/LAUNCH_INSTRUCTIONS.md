# 🚀 Launch Ocelot on Your Mac

## Quick Launch (Easiest)

### Method 1: Double-Click Launcher

1. **Download:** `LAUNCH_OCELOT.command`
2. **Double-click** the file
3. **Allow execution** if macOS asks
4. **Wait** for admin panel to open in browser

That's it! 🎉

---

## Method 2: Terminal Commands

```bash
# Navigate to Ocelot directory
cd /Applications/XAMPP/xamppfiles/htdocs/ocelot

# Start tracker (choose based on your Mac)
# For Apple Silicon (M1/M2/M3):
./ocelot-tracker-arm64

# For Intel Macs:
./ocelot-tracker-intel

# Or use the auto-detect launcher:
./START.command
```

---

## Method 3: Manual Step-by-Step

### 1. Start XAMPP Apache

```bash
# Open XAMPP Control Panel
open /Applications/XAMPP/manager-osx.app
```

Click **Start** next to Apache.

### 2. Start Tracker

```bash
cd /Applications/XAMPP/xamppfiles/htdocs/ocelot

# Make sure it's executable
chmod +x ocelot-tracker-*

# Detect your Mac's architecture
arch

# Start the appropriate binary:
# If output is "arm64":
./ocelot-tracker-arm64 &

# If output is "i386" or "x86_64":
./ocelot-tracker-intel &
```

### 3. Access Admin Panel

Open browser to: **http://localhost/ocelot/admin/login.php**

---

## What the Launcher Does

```
1. ✓ Detects your Mac's CPU (Intel or Apple Silicon)
2. ✓ Checks if tracker is already running
3. ✓ Creates data/db directory if missing
4. ✓ Starts tracker on port 34000
5. ✓ Checks if XAMPP Apache is running
6. ✓ Opens admin panel in your browser
7. ✓ Shows live tracker logs
```

---

## Troubleshooting

### "Permission denied" when launching

```bash
chmod +x LAUNCH_OCELOT.command
./LAUNCH_OCELOT.command
```

Or right-click → **Open With** → **Terminal**

### macOS blocks the file

1. Right-click `LAUNCH_OCELOT.command`
2. Select **Open**
3. Click **Open** in security dialog

Or remove quarantine:
```bash
xattr -d com.apple.quarantine LAUNCH_OCELOT.command
```

### "Tracker binary not found"

Download the update package and run:
```bash
./UPDATE.sh
```

### "Apache not running"

Start XAMPP:
```bash
open /Applications/XAMPP/manager-osx.app
```

Click **Start** next to Apache.

### Port 34000 already in use

```bash
# Find process using port
lsof -ti:34000

# Kill it
lsof -ti:34000 | xargs kill -9

# Then start again
./LAUNCH_OCELOT.command
```

### Database error in admin panel

```bash
# Create database directory
mkdir -p /Applications/XAMPP/xamppfiles/htdocs/ocelot/data/db

# Check permissions
ls -la /Applications/XAMPP/xamppfiles/htdocs/ocelot/data
```

---

## Verify Everything is Running

### Check Tracker

```bash
# Should return tracker info
curl http://localhost:34000

# Test announce
curl "http://localhost:34000/0123456789abcdef0123456789abcdef/announce?info_hash=test&peer_id=12345678901234567890&port=6881&uploaded=0&downloaded=0&left=1000000&compact=1"
```

### Check Admin Panel

```bash
# Should return HTML
curl http://localhost/ocelot/admin/login.php
```

### Check Processes

```bash
# Should show tracker
ps aux | grep ocelot-tracker

# Should show PHP if using built-in server
ps aux | grep php
```

---

## Stop Everything

### Stop Tracker

```bash
lsof -ti:34000 | xargs kill -9
```

### Stop XAMPP Apache

Open XAMPP Control Panel and click **Stop** next to Apache.

---

## URLs Reference

| Service | URL |
|---------|-----|
| **Admin Login** | http://localhost/ocelot/admin/login.php |
| **Dashboard** | http://localhost/ocelot/admin/ |
| **Users** | http://localhost/ocelot/admin/users.php |
| **Torrents** | http://localhost/ocelot/admin/torrents.php |
| **Peers** | http://localhost/ocelot/admin/peers.php |
| **Stats** | http://localhost/ocelot/admin/stats.php |
| **Tracker** | http://localhost:34000 |
| **XAMPP** | http://localhost/dashboard/ |

---

## Default Credentials

**Admin Panel:**
- Username: `admin`
- Password: `changeme`

**Sample Passkey:**
- `0123456789abcdef0123456789abcdef`

---

## Logs Location

- **Tracker:** `/Applications/XAMPP/xamppfiles/htdocs/ocelot/tracker.log`
- **Apache:** `/Applications/XAMPP/xamppfiles/logs/error_log`

Watch live:
```bash
tail -f /Applications/XAMPP/xamppfiles/htdocs/ocelot/tracker.log
```

---

## Next Steps After Launch

1. ✅ Login to admin panel
2. ✅ Change admin password
3. ✅ Test drag-and-drop (.torrent upload)
4. ✅ Add real users and torrents
5. ✅ Monitor dashboard statistics

---

**Just double-click `LAUNCH_OCELOT.command` to start everything!** 🚀
