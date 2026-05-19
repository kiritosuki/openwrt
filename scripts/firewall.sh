#!/bin/sh
set -eu

PREFIX="cnslab_"

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

usage() {
  cat >&2 <<'EOF'
Usage:
  firewall.sh list
  firewall.sh add --proto tcp|udp|icmp|all [--src IP/CIDR] [--dest IP/CIDR] [--port PORT[-PORT]] --action accept|reject|drop
  firewall.sh delete --name cnslab_NAME
  firewall.sh clear
  firewall.sh verify --host HOST --port PORT
EOF
}

need_uci() {
  command -v uci >/dev/null 2>&1 || {
    echo "uci command not found; run this script on OpenWrt" >&2
    exit 127
  }
}

reload_firewall() {
  /etc/init.d/firewall reload >/dev/null 2>&1 || fw4 reload >/dev/null 2>&1 || true
}

list_rules() {
  need_uci
  first=1
  printf '['
  for section in $(uci show firewall 2>/dev/null | sed -n "s/^firewall\\.\\([^.=]*\\)\\.name='${PREFIX}.*$/\\1/p"); do
    name=$(uci -q get "firewall.$section.name" || true)
    proto=$(uci -q get "firewall.$section.proto" || true)
    src_ip=$(uci -q get "firewall.$section.src_ip" || true)
    dest_ip=$(uci -q get "firewall.$section.dest_ip" || true)
    dest_port=$(uci -q get "firewall.$section.dest_port" || true)
    target=$(uci -q get "firewall.$section.target" || true)
    action=$(printf '%s' "$target" | tr 'A-Z' 'a-z')

    [ "$first" -eq 1 ] || printf ','
    first=0
    printf '{"name":"%s","proto":"%s","src_ip":"%s","dest_ip":"%s","port":"%s","action":"%s"}' \
      "$(json_escape "$name")" "$(json_escape "$proto")" "$(json_escape "$src_ip")" \
      "$(json_escape "$dest_ip")" "$(json_escape "$dest_port")" "$(json_escape "$action")"
  done
  printf ']\n'
}

add_rule() {
  need_uci
  proto=""
  src_ip=""
  dest_ip=""
  port=""
  action=""

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --proto) proto="${2:-}"; shift 2 ;;
      --src) src_ip="${2:-}"; shift 2 ;;
      --dest) dest_ip="${2:-}"; shift 2 ;;
      --port) port="${2:-}"; shift 2 ;;
      --action) action="${2:-}"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done

  case "$proto" in tcp|udp|icmp|all) ;; *) echo "invalid proto" >&2; exit 2 ;; esac
  case "$action" in
    accept) target="ACCEPT" ;;
    reject) target="REJECT" ;;
    drop) target="DROP" ;;
    *) echo "invalid action" >&2; exit 2 ;;
  esac

  name="${PREFIX}$(date +%Y%m%d%H%M%S)_$$"
  section=$(uci add firewall rule)
  uci set "firewall.$section.name=$name"
  uci set "firewall.$section.src=lan"
  uci set "firewall.$section.dest=wan"
  uci set "firewall.$section.target=$target"
  [ "$proto" = "all" ] || uci set "firewall.$section.proto=$proto"
  [ -z "$src_ip" ] || uci set "firewall.$section.src_ip=$src_ip"
  [ -z "$dest_ip" ] || uci set "firewall.$section.dest_ip=$dest_ip"
  [ -z "$port" ] || uci set "firewall.$section.dest_port=$port"
  uci set "firewall.$section.family=ipv4"
  uci set "firewall.$section.enabled=1"
  uci commit firewall
  reload_firewall
  printf '{"name":"%s","message":"rule added and firewall reloaded"}\n' "$(json_escape "$name")"
}

delete_rule() {
  need_uci
  name=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --name) name="${2:-}"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  case "$name" in ${PREFIX}*) ;; *) echo "invalid name" >&2; exit 2 ;; esac

  found=0
  for section in $(uci show firewall 2>/dev/null | sed -n "s/^firewall\\.\\([^.=]*\\)\\.name='$(printf '%s' "$name" | sed 's/[.[\*^$()+?{}|]/\\&/g')'$/\\1/p"); do
    uci delete "firewall.$section"
    found=1
  done
  [ "$found" -eq 1 ] || { echo "rule not found" >&2; exit 1; }
  uci commit firewall
  reload_firewall
  printf '{"message":"rule deleted"}\n'
}

clear_rules() {
  need_uci
  changed=0
  for section in $(uci show firewall 2>/dev/null | sed -n "s/^firewall\\.\\([^.=]*\\)\\.name='${PREFIX}.*$/\\1/p"); do
    uci delete "firewall.$section"
    changed=1
  done
  if [ "$changed" -eq 1 ]; then
    uci commit firewall
    reload_firewall
  fi
  printf '{"message":"experiment rules cleared"}\n'
}

verify_target() {
  host=""
  port=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --host) host="${2:-}"; shift 2 ;;
      --port) port="${2:-}"; shift 2 ;;
      *) usage; exit 2 ;;
    esac
  done
  [ -n "$host" ] && [ -n "$port" ] || { usage; exit 2; }

  if command -v nc >/dev/null 2>&1; then
    if nc -z -w 3 "$host" "$port" >/dev/null 2>&1; then
      printf '{"reachable":true,"method":"nc","target":"%s:%s"}\n' "$(json_escape "$host")" "$(json_escape "$port")"
    else
      printf '{"reachable":false,"method":"nc","target":"%s:%s"}\n' "$(json_escape "$host")" "$(json_escape "$port")"
      exit 1
    fi
  else
    ping -c 1 -W 3 "$host"
  fi
}

cmd="${1:-}"
[ "$#" -gt 0 ] && shift || true
case "$cmd" in
  list) list_rules "$@" ;;
  add) add_rule "$@" ;;
  delete) delete_rule "$@" ;;
  clear) clear_rules "$@" ;;
  verify) verify_target "$@" ;;
  *) usage; exit 2 ;;
esac
