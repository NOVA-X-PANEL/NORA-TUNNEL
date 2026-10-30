#!/usr/bin/env bash
# ==============================================================================
# NORA TUNNEL — Installer & Auto-Deployment Script
# Author: NOVA-X Team
# Repository: https://github.com/NOVA-X-PANEL/NORA-TUNNEL
# ==============================================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/nora-tunnel"
SERVICE_FILE="/etc/systemd/system/nora-tunnel.service"
GITHUB_REPO="NOVA-X-PANEL/NORA-TUNNEL"
NORA_MIRROR=""

print_banner() {
    clear
    echo -e "${CYAN}${BOLD}"
    cat << "EOF"
  _  _  ___  ____    _     _____ _   _ _   _ _   _ _____ _     
 | \| |/ _ \|  _ \  / \   |_   _| | | | \ | | \ | | ____| |    
 | .` | | | | |_) |/ _ \    | | | | | |  \| |  \| |  _| | |    
 | |\ | |_| |  _ </ ___ \   | | | |_| | |\  | |\  | |___| |___ 
 |_| \_|\___/|_| \_\_/   \_\  |_|  \___/|_| \_|_| \_|_____|_____|
                                                                
EOF
    echo -e "${MAGENTA}   »»» اتوماسیون تانل فوق‌سریع و هوشمند بک‌هال نورا (NORA TUNNEL) «««${NC}"
    echo -e "${CYAN}------------------------------------------------------------------${NC}"
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        echo -e "${RED}[ERROR] لطفاً این اسکریپت را با دسترسی روت (root) اجرا کنید.${NC}"
        exit 1
    fi
}

detect_arch() {
    local arch=$(uname -m)
    case "$arch" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        *)
            echo -e "${RED}[ERROR] معماری پردازنده ($arch) پشتیبانی نمی‌شود.${NC}"
            exit 1
            ;;
    esac
}

install_deps() {
    echo -e "${YELLOW}[+] در حال بررسی و نصب بسته‌های مورد نیاز...${NC}"
    if command -v apt-get &>/dev/null; then
        apt-get update -y >/dev/null 2>&1 || true
        apt-get install -y curl tar wget iptables >/dev/null 2>&1 || true
    elif command -v yum &>/dev/null; then
        yum install -y curl tar wget iptables >/dev/null 2>&1 || true
    fi
}

install_binary() {
    echo -e "${YELLOW}[+] در حال دانلود و راه‌اندازی NORA TUNNEL (${ARCH})...${NC}"
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$CONFIG_DIR/backhaul"

    # If running locally or pre-compiled binary exists
    if [ -f "./nora-tunnel" ]; then
        cp ./nora-tunnel "$INSTALL_DIR/nora-tunnel"
        chmod +x "$INSTALL_DIR/nora-tunnel"
    elif [ -n "$NORA_MIRROR" ]; then
        echo -e "${CYAN}[*] در حال دریافت باینری نورا از سرور (${NORA_MIRROR})...${NC}"
        if curl -fsSL "http://${NORA_MIRROR}/download/nora-tunnel-linux-${ARCH}" -o "$INSTALL_DIR/nora-tunnel"; then
            chmod +x "$INSTALL_DIR/nora-tunnel"
        fi
    else
        # Download from GitHub Release
        local LATEST_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/nora-tunnel-linux-${ARCH}.tar.gz"
        echo -e "${CYAN}[*] دریافت از: ${LATEST_URL}${NC}"
        if curl -fsSL "$LATEST_URL" -o /tmp/nora-tunnel.tar.gz 2>/dev/null; then
            tar -xzf /tmp/nora-tunnel.tar.gz -C "$INSTALL_DIR"
            chmod +x "$INSTALL_DIR/nora-tunnel"
            rm -f /tmp/nora-tunnel.tar.gz
        fi
    fi

    # Download backhaul binary if not exists
    if ! command -v backhaul &>/dev/null && [ ! -f "$INSTALL_DIR/backhaul" ]; then
        echo -e "${YELLOW}[+] در حال نصب هسته Backhaul v0.7.2...${NC}"
        if [ -n "$NORA_MIRROR" ] && curl -fsSL "http://${NORA_MIRROR}/download/backhaul" -o "$INSTALL_DIR/backhaul" 2>/dev/null; then
            chmod +x "$INSTALL_DIR/backhaul"
        else
            local BH_URL="https://github.com/Musixal/Backhaul/releases/download/v0.7.2/backhaul_linux_${ARCH}.tar.gz"
            if curl -fsSL "$BH_URL" -o /tmp/backhaul.tar.gz; then
                tar -xzf /tmp/backhaul.tar.gz -C /tmp
                mv /tmp/backhaul "$INSTALL_DIR/backhaul"
                chmod +x "$INSTALL_DIR/backhaul"
                rm -f /tmp/backhaul.tar.gz
            fi
        fi
    fi
}

setup_systemd() {
    echo -e "${YELLOW}[+] در حال تنظیم سرویس systemd...${NC}"
    cat << EOF > "$SERVICE_FILE"
[Unit]
Description=NORA TUNNEL Management Service
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/nora-tunnel
ExecStart=$INSTALL_DIR/nora-tunnel -c $CONFIG_DIR/config.json
Restart=always
RestartSec=3s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable nora-tunnel
    systemctl restart nora-tunnel
}

setup_cli() {
    cat << 'EOF' > "$INSTALL_DIR/nora"
#!/usr/bin/env bash
case "$1" in
    start)
        systemctl start nora-tunnel
        echo "NORA TUNNEL started."
        ;;
    stop)
        systemctl stop nora-tunnel
        echo "NORA TUNNEL stopped."
        ;;
    restart)
        systemctl restart nora-tunnel
        echo "NORA TUNNEL restarted."
        ;;
    status)
        systemctl status nora-tunnel
        ;;
    log|logs)
        journalctl -u nora-tunnel -f -n 50
        ;;
    passwd|password)
        read -rp "نام کاربری جدید: " u
        read -rp "رمز عبور جدید: " p
        /usr/local/bin/nora-tunnel -c /etc/nora-tunnel/config.json -set-user "$u" -set-pass "$p"
        systemctl restart nora-tunnel
        ;;
    *)
        echo "NORA TUNNEL CLI"
        echo "Usage: nora {start|stop|restart|status|logs|passwd}"
        ;;
esac
EOF
    chmod +x "$INSTALL_DIR/nora"
}

configure_credentials() {
    echo -e "${YELLOW}------------------------------------------------------------------${NC}"
    echo -e "${CYAN}${BOLD}[+] Panel Credentials Setup / تنظیم اطلاعات ورود به پنل:${NC}"

    local input_user=""
    local input_pass=""

    echo -ne "${YELLOW}Enter Admin Username [Default: admin]: ${NC}"
    if [ -c /dev/tty ]; then
        read -r input_user </dev/tty 2>/dev/null || read -r input_user || true
    else
        read -r input_user 2>/dev/null || true
    fi

    echo -ne "${YELLOW}Enter Admin Password [Default: admin]: ${NC}"
    if [ -c /dev/tty ]; then
        read -r input_pass </dev/tty 2>/dev/null || read -r input_pass || true
    else
        read -r input_pass 2>/dev/null || true
    fi

    ADMIN_USER="${input_user:-admin}"
    ADMIN_PASS="${input_pass:-admin}"

    $INSTALL_DIR/nora-tunnel -c $CONFIG_DIR/config.json -set-user "$ADMIN_USER" -set-pass "$ADMIN_PASS" >/dev/null 2>&1 || true
    systemctl restart nora-tunnel
    echo -e "${GREEN}[✔] Username: ${ADMIN_USER} | Password set successfully.${NC}"
}

get_public_ip() {
    SERVER_IP=$(curl -s -4 https://api.ipify.org || curl -s -4 https://ifconfig.me || echo "SERVER_IP")
}

main() {
    print_banner
    check_root
    detect_arch
    install_deps
    install_binary
    setup_systemd
    setup_cli
    configure_credentials
    get_public_ip

    echo -e "${GREEN}==================================================================${NC}"
    echo -e "${GREEN}${BOLD}✔ نصب NORA TUNNEL با موفقیت انجام شد!${NC}"
    echo -e "${GREEN}==================================================================${NC}"
    echo -e "${CYAN}🌐 نشانی پنل وب:${NC}  ${BOLD}http://${SERVER_IP}:7777/${NC}"
    echo -e "${CYAN}👤 نام کاربری:${NC}   ${BOLD}${ADMIN_USER}${NC}"
    echo -e "${CYAN}🔑 رمز عبور:${NC}     ${BOLD}${ADMIN_PASS}${NC}"
    echo -e "${MAGENTA}------------------------------------------------------------------${NC}"
    echo -e "${YELLOW}راهنمای اتصال سریع:${NC}"
    echo -e " ۱. در سرور خارج وارد پنل شده و آی‌پی سرور ایران را وارد کنید."
    echo -e " ۲. دکمه «تولید هش اختصاصی تانل» را بزنید و هش NORA:// را کپی کنید."
    echo -e " ۳. در سرور ایران وارد پنل شده و هش را در بخش «اتصال سریع» پیست کنید."
    echo -e " ۴. تانل متصل شده و تمامی پورت‌ها به صورت خودکار فوروارد می‌شوند!"
    echo -e "${GREEN}==================================================================${NC}"
}

main "$@"
