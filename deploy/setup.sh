#!/usr/bin/env bash
# Ocelot VPS provisioning script — IONOS VPS (Debian/Ubuntu)
# Run as root on a fresh server:  bash setup.sh
set -euo pipefail

OCELOT_USER=ocelot
OCELOT_HOME=/var/lib/ocelot
OCELOT_CONF=/etc/ocelot
OCELOT_LOG=/var/log/ocelot
BINARY_DST=/usr/local/bin/ocelot
TRACKER_PORT=34000
METRICS_PORT=6880

info()  { echo "  [INFO]  $*"; }
warn()  { echo "  [WARN]  $*"; }
die()   { echo "  [ERROR] $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "This script must be run as root."

# ── Dependencies ──────────────────────────────────────────────────────────────
info "Updating package index..."
apt-get update -qq

info "Installing ufw if not present..."
apt-get install -y -qq ufw

# ── System user ───────────────────────────────────────────────────────────────
if ! id "$OCELOT_USER" &>/dev/null; then
    info "Creating system user: $OCELOT_USER"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$OCELOT_USER"
fi

# ── Directories ───────────────────────────────────────────────────────────────
info "Creating directories..."
mkdir -p "$OCELOT_HOME/db" "$OCELOT_CONF/tls" "$OCELOT_LOG"
chown -R "$OCELOT_USER:$OCELOT_USER" "$OCELOT_HOME" "$OCELOT_LOG"
chmod 750 "$OCELOT_HOME" "$OCELOT_LOG"
chmod 700 "$OCELOT_CONF/tls"

# ── Config file ───────────────────────────────────────────────────────────────
if [[ ! -f "$OCELOT_CONF/ocelot.conf" ]]; then
    if [[ -f "$(dirname "$0")/ocelot.conf.example" ]]; then
        info "Installing example config to $OCELOT_CONF/ocelot.conf"
        cp "$(dirname "$0")/ocelot.conf.example" "$OCELOT_CONF/ocelot.conf"
        chmod 640 "$OCELOT_CONF/ocelot.conf"
        chown root:"$OCELOT_USER" "$OCELOT_CONF/ocelot.conf"
        warn "Edit $OCELOT_CONF/ocelot.conf — set site_password and report_password before starting."
    else
        warn "No ocelot.conf.example found.  Create $OCELOT_CONF/ocelot.conf manually."
    fi
else
    info "Config already exists at $OCELOT_CONF/ocelot.conf — skipping."
fi

# ── Binary ───────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_SRC="$SCRIPT_DIR/../ocelot"
if [[ -f "$BINARY_SRC" ]]; then
    info "Installing binary to $BINARY_DST"
    install -m 755 "$BINARY_SRC" "$BINARY_DST"
else
    warn "Binary not found at $BINARY_SRC.  Copy the ocelot binary to $BINARY_DST manually."
fi

# ── systemd service ───────────────────────────────────────────────────────────
SERVICE_SRC="$SCRIPT_DIR/ocelot.service"
if [[ -f "$SERVICE_SRC" ]]; then
    info "Installing systemd service..."
    cp "$SERVICE_SRC" /etc/systemd/system/ocelot.service
    systemctl daemon-reload
    systemctl enable ocelot.service
    info "Service installed.  Start with:  systemctl start ocelot"
else
    warn "ocelot.service not found.  Install the systemd unit manually."
fi

# ── Firewall (ufw) ────────────────────────────────────────────────────────────
info "Configuring ufw rules..."

# Allow SSH first so we don't lock ourselves out
ufw allow OpenSSH

# BitTorrent tracker — TCP + UDP on the tracker port
ufw allow "$TRACKER_PORT/tcp"  comment "Ocelot HTTP tracker"
ufw allow "$TRACKER_PORT/udp"  comment "Ocelot UDP tracker (BEP-15)"

# Metrics/events port — allow only from localhost by default.
# To expose to a monitoring host: ufw allow from <monitor-ip> to any port 6880
ufw deny "$METRICS_PORT"  comment "Ocelot metrics (localhost only)"

# Enable ufw if not already enabled
if ufw status | grep -q "Status: inactive"; then
    info "Enabling ufw..."
    ufw --force enable
fi

ufw status verbose

# ── sysctl tuning ─────────────────────────────────────────────────────────────
SYSCTL_CONF=/etc/sysctl.d/60-ocelot.conf
if [[ ! -f "$SYSCTL_CONF" ]]; then
    info "Writing sysctl tuning to $SYSCTL_CONF"
    cat > "$SYSCTL_CONF" <<'SYSCTL'
# Ocelot tracker — network tuning
net.core.somaxconn          = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.ipv4.ip_local_port_range = 1024 65535
net.core.netdev_max_backlog  = 5000
net.ipv4.tcp_fin_timeout     = 15
net.ipv4.tcp_tw_reuse        = 1
fs.file-max                  = 200000
SYSCTL
    sysctl --system -q
fi

# ── File descriptor limit ─────────────────────────────────────────────────────
LIMITS_CONF=/etc/security/limits.d/ocelot.conf
if [[ ! -f "$LIMITS_CONF" ]]; then
    info "Writing ulimit config to $LIMITS_CONF"
    cat > "$LIMITS_CONF" <<LIMITS
$OCELOT_USER soft nofile 65536
$OCELOT_USER hard nofile 65536
LIMITS
fi

# ── Done ─────────────────────────────────────────────────────────────────────
echo ""
echo "  Setup complete.  Next steps:"
echo "    1. Edit $OCELOT_CONF/ocelot.conf  (set passwords, db_dir, gazelle_url)"
echo "    2. systemctl start ocelot"
echo "    3. journalctl -u ocelot -f"
echo ""
