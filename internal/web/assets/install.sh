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
    echo -e "${MAGENTA}       »»» NORA TUNNEL — Smart Layer-4 Tunnel Automation «««${NC}"
    echo -e "${CYAN}------------------------------------------------------------------${NC}"
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        echo -e "${RED}[ERROR] Please run this installer as root.${NC}"
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
            echo -e "${RED}[ERROR] Architecture ($arch) is not supported.${NC}"
            exit 1
            ;;
    esac
}

install_deps() {
    echo -e "${YELLOW}[+] Updating package lists and installing dependencies...${NC}"
    if command -v apt-get &>/dev/null; then
        apt-get update -y >/dev/null 2>&1 || true
        apt-get install -y curl tar wget iptables >/dev/null 2>&1 || true
    elif command -v yum &>/dev/null; then
        yum install -y curl tar wget iptables >/dev/null 2>&1 || true
    fi
}

install_binary() {
    echo -e "${YELLOW}[+] Stopping existing NORA TUNNEL instances...${NC}"
    systemctl stop nora-tunnel 2>/dev/null || true
    pkill -9 nora-tunnel 2>/dev/null || true
    fuser -k 7777/tcp 2>/dev/null || true

    echo -e "${YELLOW}[+] Downloading and setting up NORA TUNNEL (${ARCH})...${NC}"
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$CONFIG_DIR/backhaul"

    # If running locally or pre-compiled binary exists
    if [ -f "./nora-tunnel" ]; then
        cp ./nora-tunnel "$INSTALL_DIR/nora-tunnel"
        chmod +x "$INSTALL_DIR/nora-tunnel"
    elif [ -n "$NORA_MIRROR" ]; then
        echo -e "${CYAN}[*] Downloading binary from mirror (${NORA_MIRROR})...${NC}"
        if curl -fsSL "http://${NORA_MIRROR}/download/nora-tunnel-linux-${ARCH}" -o "$INSTALL_DIR/nora-tunnel"; then
            chmod +x "$INSTALL_DIR/nora-tunnel"
        fi
    else
        # Download from GitHub Release
        local LATEST_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/nora-tunnel-linux-${ARCH}.tar.gz"
        echo -e "${CYAN}[*] Downloading from: ${LATEST_URL}${NC}"
        if curl -fsSL "$LATEST_URL" -o /tmp/nora-tunnel.tar.gz 2>/dev/null; then
            tar -xzf /tmp/nora-tunnel.tar.gz -C "$INSTALL_DIR"
            chmod +x "$INSTALL_DIR/nora-tunnel"
            rm -f /tmp/nora-tunnel.tar.gz
        fi
    fi

    # Download backhaul binary if not exists
    if ! command -v backhaul &>/dev/null && [ ! -f "$INSTALL_DIR/backhaul" ]; then
        echo -e "${YELLOW}[+] Installing Backhaul core v0.7.2...${NC}"
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
    echo -e "${YELLOW}[+] Configuring systemd service...${NC}"
    cat << EOF > "$SERVICE_FILE"
[Unit]
Description=NORA TUNNEL Management Service
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/etc/nora-tunnel
ExecStartPre=/bin/sh -c 'fuser -k 7777/tcp 2>/dev/null || true'
ExecStart=$INSTALL_DIR/nora-tunnel -c $CONFIG_DIR/config.json
Restart=always
RestartSec=3s
TimeoutStopSec=5s
KillMode=mixed
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable nora-tunnel
    systemctl restart nora-tunnel
}

setup_cli() {
    echo -e "${YELLOW}[+] Installing CLI management menu ('nora')...${NC}"
    cat << 'EOF' > "$INSTALL_DIR/nora"
#!/usr/bin/env bash
# NORA TUNNEL — CLI Management Tool

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/nora-tunnel"
SERVICE_NAME="nora-tunnel"

show_banner() {
    clear
    echo -e "${CYAN}${BOLD}"
    cat << "BANNER"
  _  _  ___  ____    _     _____ _   _ _   _ _   _ _____ _     
 | \| |/ _ \|  _ \  / \   |_   _| | | | \ | | \ | | ____| |    
 | .` | | | | |_) |/ _ \    | | | | | |  \| |  \| |  _| | |    
 | |\ | |_| |  _ </ ___ \   | | | |_| | |\  | |\  | |___| |___ 
 |_| \_|\___/|_| \_\_/   \_\  |_|  \___/|_| \_|_| \_|_____|_____|
BANNER
    echo -e "${MAGENTA}       »»» NORA TUNNEL — Layer-4 Tunnel Management CLI «««${NC}"
    echo -e "${CYAN}------------------------------------------------------------------${NC}"
}

check_root() {
    if [ "$EUID" -ne 0 ]; then
        echo -e "${RED}[ERROR] Please run this command as root.${NC}"
        exit 1
    fi
}

service_status() {
    echo -e "${CYAN}[*] Checking ${SERVICE_NAME} status:${NC}"
    systemctl status ${SERVICE_NAME} --no-pager
}

service_start() {
    echo -e "${YELLOW}[+] Starting service...${NC}"
    systemctl start ${SERVICE_NAME}
    echo -e "${GREEN}[✔] Service started successfully.${NC}"
}

service_stop() {
    echo -e "${YELLOW}[+] Stopping service...${NC}"
    systemctl stop ${SERVICE_NAME}
    echo -e "${GREEN}[✔] Service stopped successfully.${NC}"
}

service_restart() {
    echo -e "${YELLOW}[+] Restarting service...${NC}"
    systemctl restart ${SERVICE_NAME}
    echo -e "${GREEN}[✔] Service restarted successfully.${NC}"
}

service_logs() {
    echo -e "${CYAN}[*] Displaying live logs (Press Ctrl+C to exit):${NC}"
    journalctl -u ${SERVICE_NAME} -f -n 50
}

change_credentials() {
    echo -e "${YELLOW}------------------------------------------------------------------${NC}"
    echo -e "${CYAN}${BOLD}[+] Change Web Panel Credentials:${NC}"
    read -rp "Enter new username: " new_user
    read -rp "Enter new password: " new_pass

    if [ -z "$new_user" ] || [ -z "$new_pass" ]; then
        echo -e "${RED}[ERROR] Username or password cannot be empty.${NC}"
        return
    fi

    ${INSTALL_DIR}/nora-tunnel -c ${CONFIG_DIR}/config.json -set-user "$new_user" -set-pass "$new_pass"
    systemctl restart ${SERVICE_NAME}
    echo -e "${GREEN}[✔] Credentials updated successfully.${NC}"
}

update_panel() {
    echo -e "${YELLOW}[+] Fetching latest release from GitHub...${NC}"
    bash <(curl -fsSL https://raw.githubusercontent.com/NOVA-X-PANEL/NORA-TUNNEL/main/install.sh)
}

reinstall_panel() {
    echo -ne "${YELLOW}[!] Are you sure you want to reinstall NORA TUNNEL? [y/N]: ${NC}"
    read -r confirm
    if [[ "$confirm" =~ ^[yY]$ ]]; then
        bash <(curl -fsSL https://raw.githubusercontent.com/NOVA-X-PANEL/NORA-TUNNEL/main/install.sh)
    else
        echo -e "${CYAN}[*] Operation cancelled.${NC}"
    fi
}

uninstall_panel() {
    echo -e "${RED}${BOLD}[!] WARNING: This will completely remove NORA TUNNEL and all services.${NC}"
    read -rp "Are you sure? Type 'yes' to confirm: " confirm
    if [ "$confirm" == "yes" ]; then
        echo -e "${YELLOW}[+] Stopping service...${NC}"
        systemctl stop ${SERVICE_NAME} 2>/dev/null || true
        systemctl disable ${SERVICE_NAME} 2>/dev/null || true
        rm -f /etc/systemd/system/${SERVICE_NAME}.service
        systemctl daemon-reload

        echo -e "${YELLOW}[+] Removing binaries...${NC}"
        rm -f ${INSTALL_DIR}/nora-tunnel
        rm -f ${INSTALL_DIR}/nora

        read -rp "Do you also want to remove config directory (/etc/nora-tunnel)? [y/N]: " del_cfg
        if [[ "$del_cfg" =~ ^[yY]$ ]]; then
            rm -rf ${CONFIG_DIR}
        fi

        echo -e "${GREEN}[✔] NORA TUNNEL completely uninstalled.${NC}"
        exit 0
    else
        echo -e "${CYAN}[*] Uninstall cancelled.${NC}"
    fi
}

show_menu() {
    while true; do
        show_banner

        local st_text="${RED}[ STOPPED ]${NC}"
        if systemctl is-active --quiet ${SERVICE_NAME}; then
            st_text="${GREEN}[ RUNNING ]${NC}"
        fi

        local srv_ip=$(curl -s -4 https://api.ipify.org 2>/dev/null || echo "SERVER_IP")

        echo -e "  Service Status : ${st_text}"
        echo -e "  Dashboard URL  : ${BOLD}http://${srv_ip}:7777/${NC}"
        echo -e "${CYAN}------------------------------------------------------------------${NC}"
        echo -e "  ${GREEN}1)${NC} Service Status"
        echo -e "  ${GREEN}2)${NC} Start Service"
        echo -e "  ${GREEN}3)${NC} Stop Service"
        echo -e "  ${GREEN}4)${NC} Restart Service"
        echo -e "  ${GREEN}5)${NC} View Live Logs"
        echo -e "  ${GREEN}6)${NC} Change Admin Password"
        echo -e "  ${CYAN}7)${NC} Update NORA TUNNEL"
        echo -e "  ${YELLOW}8)${NC} Reinstall Panel"
        echo -e "  ${RED}9)${NC} Completely Uninstall"
        echo -e "  ${MAGENTA}0)${NC} Exit Menu"
        echo -e "${CYAN}------------------------------------------------------------------${NC}"
        echo -ne "Please select an option [0-9]: "
        read -r choice

        case "$choice" in
            1) service_status; echo -ne "\nPress Enter to return to menu..."; read -r ;;
            2) service_start; sleep 1 ;;
            3) service_stop; sleep 1 ;;
            4) service_restart; sleep 1 ;;
            5) service_logs ;;
            6) change_credentials; echo -ne "\nPress Enter to return to menu..."; read -r ;;
            7) update_panel; break ;;
            8) reinstall_panel; break ;;
            9) uninstall_panel; break ;;
            0|q|exit) echo -e "\n${CYAN}Exiting menu.${NC}"; exit 0 ;;
            *) echo -e "${RED}Invalid option!${NC}"; sleep 1 ;;
        esac
    done
}

main() {
    check_root

    case "$1" in
        status) service_status ;;
        start) service_start ;;
        stop) service_stop ;;
        restart) service_restart ;;
        log|logs) service_logs ;;
        passwd|password) change_credentials ;;
        update) update_panel ;;
        reinstall) reinstall_panel ;;
        uninstall) uninstall_panel ;;
        "") show_menu ;;
        *)
            echo "NORA TUNNEL CLI"
            echo "Usage: nora {status|start|stop|restart|logs|passwd|update|reinstall|uninstall}"
            echo "Or run 'nora' without arguments to open interactive menu."
            ;;
    esac
}

main "$@"
EOF
    chmod +x "$INSTALL_DIR/nora"
}

configure_credentials() {
    ADMIN_USER="admin"
    ADMIN_PASS="admin"
    $INSTALL_DIR/nora-tunnel -c $CONFIG_DIR/config.json -set-user "$ADMIN_USER" -set-pass "$ADMIN_PASS" >/dev/null 2>&1 || true
    systemctl restart nora-tunnel
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
    echo -e "${GREEN}${BOLD}✔ NORA TUNNEL installed successfully!${NC}"
    echo -e "${GREEN}==================================================================${NC}"
    echo -e "${CYAN}🌐 Web Dashboard :${NC} ${BOLD}http://${SERVER_IP}:7777/${NC}"
    echo -e "${CYAN}👤 Username      :${NC} ${BOLD}admin${NC}"
    echo -e "${CYAN}🔑 Password      :${NC} ${BOLD}admin${NC}"
    echo -e "${MAGENTA}------------------------------------------------------------------${NC}"
    echo -e "${YELLOW}💻 CLI Management Menu:${NC} Type ${BOLD}nora${NC} in your terminal."
    echo -e "${MAGENTA}------------------------------------------------------------------${NC}"
    echo -e "${YELLOW}Quick Connection Guide:${NC}"
    echo -e " 1. Open Foreign (Kharej) server panel and enter Iran IP."
    echo -e " 2. Click 'Generate Connection Hash' and copy the NORA:// code."
    echo -e " 3. Open Iran server panel, paste hash under 'Quick Connect' and click Connect."
    echo -e " 4. Tunnel connects instantly with automatic port forwarding!"
    echo -e "${GREEN}==================================================================${NC}"
}

main "$@"
