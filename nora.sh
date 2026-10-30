#!/usr/bin/env bash
# ==============================================================================
# NORA TUNNEL — CLI Management Tool
# Repository: https://github.com/NOVA-X-PANEL/NORA-TUNNEL
# ==============================================================================

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
    cat << "EOF"
  _  _  ___  ____    _     _____ _   _ _   _ _   _ _____ _     
 | \| |/ _ \|  _ \  / \   |_   _| | | | \ | | \ | | ____| |    
 | .` | | | | |_) |/ _ \    | | | | | |  \| |  \| |  _| | |    
 | |\ | |_| |  _ </ ___ \   | | | |_| | |\  | |\  | |___| |___ 
 |_| \_|\___/|_| \_\_/   \_\  |_|  \___/|_| \_|_| \_|_____|_____|
EOF
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

        # Check running status
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
