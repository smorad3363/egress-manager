#!/usr/bin/env sh
set -eu

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

[ "$(uname -s)" = "Linux" ] || fail "network lab requires Linux"
[ "$(id -u)" -eq 0 ] || fail "network lab requires root or a privileged container"

for command_name in ip nft socat sysctl timeout grep awk; do
    require_command "$command_name"
done

suffix=$$
client_ns="egm-c-$suffix"
router_ns="egm-r-$suffix"
server_ns="egm-s-$suffix"
client_host_if="egc$suffix"
router_client_if="egrc$suffix"
router_server_if="egrs$suffix"
server_host_if="egs$suffix"
tcp_pid=""
udp_pid=""

namespace_exists() {
    ip netns list | awk '{print $1}' | grep -Fxq "$1"
}

cleanup() {
    for process_id in "$tcp_pid" "$udp_pid"; do
        if [ -n "$process_id" ] && kill -0 "$process_id" 2>/dev/null; then
            if ! kill "$process_id" 2>/dev/null; then
                :
            fi
        fi
    done

    for namespace in "$client_ns" "$router_ns" "$server_ns"; do
        if namespace_exists "$namespace"; then
            ip netns delete "$namespace"
        fi
    done
}

trap cleanup EXIT INT TERM

for namespace in "$client_ns" "$router_ns" "$server_ns"; do
    if namespace_exists "$namespace"; then
        fail "namespace already exists: $namespace"
    fi
    ip netns add "$namespace"
    ip -n "$namespace" link set lo up
done

ip link add "$client_host_if" type veth peer name "$router_client_if"
ip link set "$client_host_if" netns "$client_ns"
ip link set "$router_client_if" netns "$router_ns"
ip -n "$client_ns" link set "$client_host_if" name c0
ip -n "$router_ns" link set "$router_client_if" name r0

ip link add "$router_server_if" type veth peer name "$server_host_if"
ip link set "$router_server_if" netns "$router_ns"
ip link set "$server_host_if" netns "$server_ns"
ip -n "$router_ns" link set "$router_server_if" name r1
ip -n "$server_ns" link set "$server_host_if" name s0

ip -n "$client_ns" address add 10.203.1.2/24 dev c0
ip -n "$router_ns" address add 10.203.1.1/24 dev r0
ip -n "$router_ns" address add 10.203.2.1/24 dev r1
ip -n "$server_ns" address add 10.203.2.2/24 dev s0

ip -n "$client_ns" link set c0 up
ip -n "$router_ns" link set r0 up
ip -n "$router_ns" link set r1 up
ip -n "$server_ns" link set s0 up

ip -n "$client_ns" route add default via 10.203.1.1
ip -n "$server_ns" route add default via 10.203.2.1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.ip_forward=1

ip netns exec "$router_ns" nft add table inet foreign_lab
ip netns exec "$router_ns" nft 'add chain inet foreign_lab marker'

ip netns exec "$router_ns" nft add table ip egm_lab
ip netns exec "$router_ns" nft 'add chain ip egm_lab prerouting { type nat hook prerouting priority dstnat; policy accept; }'
ip netns exec "$router_ns" nft 'add chain ip egm_lab postrouting { type nat hook postrouting priority srcnat; policy accept; }'
ip netns exec "$router_ns" nft 'add chain ip egm_lab forward { type filter hook forward priority filter; policy accept; }'
ip netns exec "$router_ns" nft 'add rule ip egm_lab prerouting ip daddr 10.203.1.1 tcp dport 19080 counter dnat to 10.203.2.2:8080 comment "egm-lab-tcp"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab prerouting ip daddr 10.203.1.1 udp dport 19053 counter dnat to 10.203.2.2:5353 comment "egm-lab-udp"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab postrouting oifname "r1" counter masquerade comment "egm-lab-masquerade"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab forward ct state established,related counter accept comment "egm-lab-established"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab forward iifname "r0" oifname "r1" ip saddr 10.203.1.0/24 ip daddr 10.203.2.2 tcp dport 8080 counter accept comment "egm-lab-allow-tcp"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab forward iifname "r0" oifname "r1" ip saddr 10.203.1.0/24 ip daddr 10.203.2.2 udp dport 5353 counter accept comment "egm-lab-allow-udp"'
ip netns exec "$router_ns" nft 'add rule ip egm_lab forward iifname "r0" oifname "r1" counter drop comment "egm-lab-source-drop"'

tcp_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:8080,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-tcp.log 2>&1 & echo $!')
udp_pid=$(ip netns exec "$server_ns" sh -c 'socat -T2 UDP-RECVFROM:5353,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-udp.log 2>&1 & echo $!')

sleep 0.2

route_result=$(ip -n "$client_ns" route get 10.203.2.2)
printf '%s\n' "$route_result" | grep -Fq 'via 10.203.1.1' || fail "client route does not use isolated router"

tcp_result=$(printf 'tcp-ok' | ip netns exec "$client_ns" socat - TCP:10.203.1.1:19080,connect-timeout=2)
[ "$tcp_result" = "tcp-ok" ] || fail "TCP DNAT echo failed"

udp_result=$(printf 'udp-ok' | ip netns exec "$client_ns" timeout 3 socat -T2 - UDP:10.203.1.1:19053)
[ "$udp_result" = "udp-ok" ] || fail "UDP DNAT echo failed"

ip -n "$client_ns" address add 10.204.1.2/24 dev c0
if printf 'must-drop' | ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:19080,bind=10.204.1.2 >/dev/null 2>&1; then
    fail "source-CIDR restriction accepted a disallowed source"
fi

ruleset=$(ip netns exec "$router_ns" nft list table ip egm_lab)
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm-lab-tcp' >/dev/null || fail "TCP counter did not increase"
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm-lab-udp' >/dev/null || fail "UDP counter did not increase"
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm-lab-source-drop' >/dev/null || fail "source restriction drop counter did not increase"

ip netns exec "$router_ns" nft delete table ip egm_lab
if ip netns exec "$router_ns" nft list table ip egm_lab >/dev/null 2>&1; then
    fail "owned table still exists after rollback"
fi
ip netns exec "$router_ns" nft list table inet foreign_lab >/dev/null || fail "foreign table was removed during rollback"

printf 'PASS: isolated TCP/UDP NAT, source CIDR, routing, counters, rollback, and foreign preservation\n'
