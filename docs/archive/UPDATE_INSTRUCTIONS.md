# 🔄 Update Your XAMPP Installation

Your local XAMPP installation needs the latest files with the new features.

## Quick Update

### Download Update Package

**File:** `ocelot-xampp-update.tar.gz` (13 MB)

### Method 1: Automated Update (Recommended)

```bash
# Download and extract the update package
tar -xzf ocelot-xampp-update.tar.gz
cd ocelot-xampp

# Run the update script
chmod +x UPDATE.sh
./UPDATE.sh
```

The script will:
- ✅ Backup your current installation
- ✅ Update admin panel files
- ✅ Update tracker binaries
- ✅ Set correct permissions
- ✅ Keep your data/db intact

### Method 2: Manual Update

```bash
# Extract update
tar -xzf ocelot-xampp-update.tar.gz
cd ocelot-xampp

# Copy admin panel
cp -r admin/* /Applications/XAMPP/xamppfiles/htdocs/ocelot/admin/

# Copy binaries
cp ocelot-tracker-* /Applications/XAMPP/xamppfiles/htdocs/ocelot/
chmod +x /Applications/XAMPP/xamppfiles/htdocs/ocelot/ocelot-tracker-*

# Copy launcher
cp START.command /Applications/XAMPP/xamppfiles/htdocs/ocelot/
chmod +x /Applications/XAMPP/xamppfiles/htdocs/ocelot/START.command
```

## What's New

### 1. Fixed Database Path ✅
- **Before:** Error: "Database directory not found: ../data/db"
- **After:** Automatically finds and creates database directory
- Works in XAMPP, Apache, and all environments

### 2. Drag-and-Drop Torrent Upload ✅
- Drop any `.torrent` file into "Add Torrent" modal
- Automatically extracts info hash
- Shows torrent metadata (name, size, files, pieces)
- No more manual hash entry!

### 3. New Files
- `admin/api/parse-torrent.php` - Torrent parser endpoint
- Updated `admin/config.php` - Fixed database path
- Updated `admin/torrents.php` - Drag-and-drop UI

## After Update

### 1. Restart Tracker

```bash
# Stop old tracker (if running)
lsof -ti:34000 | xargs kill -9

# Start new tracker
cd /Applications/XAMPP/xamppfiles/htdocs/ocelot
./START.command
```

Or double-click `START.command`

### 2. Reload Admin Panel

Open (or refresh): **http://localhost/ocelot/admin/login.php**

### 3. Test Drag-and-Drop

1. Login to admin panel
2. Go to **Torrents** page
3. Click **"Add Torrent"** button
4. **Drag any .torrent file** into the modal
5. Watch it parse and auto-fill the info hash!

## Screenshots of New Features

### Drag-and-Drop Zone
```
┌─────────────────────────────────────┐
│     Drop .torrent file here         │
│     or click to browse              │
│                                     │
│          [Upload Icon]              │
└─────────────────────────────────────┘
```

### After Upload
```
┌─────────────────────────────────────┐
│  ✓ ubuntu-22.04.iso                 │
│    4.37 GB • 1 file • 2247 pieces   │
│                                [×]  │
└─────────────────────────────────────┘

Info Hash: [a94a8fe5ccb19ba61c4c0873d391e987982fbbd3]
           ✓ Auto-filled from .torrent file
```

## Troubleshooting

### "Update script permission denied"

```bash
chmod +x UPDATE.sh
./UPDATE.sh
```

### "Directory not found"

Make sure Ocelot is installed first:
```bash
ls /Applications/XAMPP/xamppfiles/htdocs/ocelot
```

If not found, use `INSTALL_XAMPP.sh` instead of `UPDATE.sh`

### Changes not showing

1. **Hard refresh browser:** Cmd+Shift+R
2. **Clear browser cache**
3. **Check file timestamps:**
   ```bash
   ls -lt /Applications/XAMPP/xamppfiles/htdocs/ocelot/admin/
   ```

### Database error persists

```bash
# Verify database directory exists
ls -la /Applications/XAMPP/xamppfiles/htdocs/ocelot/data/db

# If missing, create it
mkdir -p /Applications/XAMPP/xamppfiles/htdocs/ocelot/data/db
```

## Rollback (if needed)

Your old installation is backed up at:
```
/Applications/XAMPP/xamppfiles/htdocs/ocelot_backup_[timestamp]
```

To rollback:
```bash
cd /Applications/XAMPP/xamppfiles/htdocs
mv ocelot ocelot_new
mv ocelot_backup_YYYYMMDD_HHMMSS ocelot
```

## Verify Update

Check these files exist:
```bash
ls /Applications/XAMPP/xamppfiles/htdocs/ocelot/admin/api/parse-torrent.php
ls /Applications/XAMPP/xamppfiles/htdocs/ocelot/admin/config.php
```

Both should be dated today.

## Support

If issues persist:
1. Check the backup was created
2. Verify file permissions
3. Review `/tmp/tracker.log` for errors
4. Check XAMPP Apache logs

---

**Download `ocelot-xampp-update.tar.gz` and run `UPDATE.sh` to get all new features!**
