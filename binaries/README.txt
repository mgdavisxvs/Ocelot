OCELOT TRACKER - PLATFORM-SPECIFIC BINARIES
===========================================

Choose the binary for your platform:

📦 MACOS
--------
• ocelot-tracker-macos-intel    → Intel Macs (x86_64)
• ocelot-tracker-macos-arm64    → Apple Silicon (M1/M2/M3)

🪟 WINDOWS
----------
• ocelot-tracker-windows.exe    → Windows 10/11 (64-bit)

🐧 LINUX
--------
• ocelot-tracker-linux-amd64    → Linux x86_64 (Ubuntu, Debian, etc.)
• ocelot-tracker-linux-arm64    → Linux ARM64 (Raspberry Pi 4+, etc.)

HOW TO USE
----------

1. Copy the appropriate binary to your Ocelot directory
2. Rename it to 'ocelot-tracker' (or 'ocelot-tracker.exe' on Windows)
3. Make executable (macOS/Linux):
   chmod +x ocelot-tracker
4. Run it:
   ./ocelot-tracker

COMPLETE PACKAGE
----------------
For the full package with admin panel, download one of:
• ocelot-macos-intel.tar.gz
• ocelot-macos-arm64.tar.gz
• ocelot-windows.zip
• ocelot-linux-amd64.tar.gz
• ocelot-linux-arm64.tar.gz

Each includes:
✓ Platform-specific tracker binary
✓ PHP admin panel (all platforms)
✓ Go source code
✓ Documentation (LOCAL_SETUP.md, QUICKSTART.md, etc.)

PREREQUISITES
-------------
Admin Panel requires PHP 8.0+ with:
• PDO, PDO_Sqlite, curl, json, mbstring

See LOCAL_SETUP.md for installation instructions.
