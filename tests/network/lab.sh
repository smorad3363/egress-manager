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

for command_name in ip nft iptables iptables-save iptables-restore haproxy sing-box socat sysctl tcpdump timeout grep awk wg wg-quick openvpn openssl ping; do
    require_command "$command_name"
done

inventory_probe=${EGRESS_INVENTORY_PROBE:-/usr/local/bin/egress-inventory-probe}
[ -x "$inventory_probe" ] || fail "inventory probe not executable: $inventory_probe"
nat_probe=${EGRESS_NAT_PROBE:-/usr/local/bin/egress-nat-probe}
[ -x "$nat_probe" ] || fail "NAT probe not executable: $nat_probe"
counter_probe=${EGRESS_COUNTER_PROBE:-/usr/local/bin/egress-counter-probe}
[ -x "$counter_probe" ] || fail "counter probe not executable: $counter_probe"
iptables_probe=${EGRESS_IPTABLES_PROBE:-/usr/local/bin/egress-iptables-probe}
[ -x "$iptables_probe" ] || fail "iptables probe not executable: $iptables_probe"
haproxy_probe=${EGRESS_HAPROXY_PROBE:-/usr/local/bin/egress-haproxy-probe}
[ -x "$haproxy_probe" ] || fail "HAProxy probe not executable: $haproxy_probe"
singbox_probe=${EGRESS_SINGBOX_PROBE:-/usr/local/bin/egress-singbox-probe}
[ -x "$singbox_probe" ] || fail "sing-box probe not executable: $singbox_probe"
routing_probe=${EGRESS_ROUTING_PROBE:-/usr/local/bin/egress-routing-probe}
[ -x "$routing_probe" ] || fail "routing probe not executable: $routing_probe"
interface_probe=${EGRESS_INTERFACE_PROBE:-/usr/local/bin/egress-interface-probe}
[ -x "$interface_probe" ] || fail "interface outbound probe not executable: $interface_probe"

"$singbox_probe" /lab/singbox-fixtures

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
tcp6_pid=""
dns_pid=""
haproxy_primary_pid=""
haproxy_secondary_pid=""
haproxy_backup_pid=""
openvpn_server_pid=""
openvpn_client_pid=""
candidate_path="/tmp/egm-nat-$suffix.nft"
iptables_candidate_path="/tmp/egm-nat-$suffix.iptables"
haproxy_candidate_path="/tmp/egm-haproxy-$suffix.cfg"
route_candidate_dir="/tmp/egm-route-$suffix"
interface_candidate_dir="/tmp/egm-interface-$suffix"
wireguard_candidate_dir="/tmp/egm-wireguard-route-$suffix"
openvpn_candidate_dir="/tmp/egm-openvpn-route-$suffix"
openvpn_runtime_dir="/tmp/egm-openvpn-$suffix"

namespace_exists() {
    ip netns list | awk '{print $1}' | grep -Fxq "$1"
}

cleanup() {
	rm -f "$candidate_path" "$iptables_candidate_path" "$haproxy_candidate_path"
	rm -rf "$route_candidate_dir" "$interface_candidate_dir" "$wireguard_candidate_dir" "$openvpn_candidate_dir" "$openvpn_runtime_dir"
    if [ -f /tmp/egm-haproxy.pid ]; then
        haproxy_pid=$(cat /tmp/egm-haproxy.pid 2>/dev/null || true)
        if [ -n "$haproxy_pid" ]; then kill "$haproxy_pid" 2>/dev/null || true; fi
    fi
    rm -f /tmp/egm-haproxy.pid /tmp/egm-haproxy-runtime.sock /tmp/egm-haproxy-managed.cfg
    for process_id in "$tcp_pid" "$udp_pid" "$tcp6_pid" "$dns_pid" "$haproxy_primary_pid" "$haproxy_secondary_pid" "$haproxy_backup_pid" "$openvpn_server_pid" "$openvpn_client_pid"; do
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

"$interface_probe" /lab/interface-fixtures "$interface_candidate_dir"
wg-quick strip "$interface_candidate_dir/wireguard.conf" >/dev/null || fail "WireGuard fixture failed native syntax validation"

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
ip -n "$server_ns" address add 10.203.2.3/24 dev s0
ip -n "$client_ns" -6 address add 2001:db8:203:1::2/64 dev c0 nodad
ip -n "$router_ns" -6 address add 2001:db8:203:1::1/64 dev r0 nodad
ip -n "$router_ns" -6 address add 2001:db8:203:2::1/64 dev r1 nodad
ip -n "$server_ns" -6 address add 2001:db8:203:2::2/64 dev s0 nodad

ip -n "$client_ns" link set c0 up
ip -n "$router_ns" link set r0 up
ip -n "$router_ns" link set r1 up
ip -n "$server_ns" link set s0 up

ip -n "$client_ns" route add default via 10.203.1.1
ip -n "$server_ns" route add default via 10.203.2.1
ip -n "$client_ns" -6 route add default via 2001:db8:203:1::1
ip -n "$server_ns" -6 route add default via 2001:db8:203:2::1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.ip_forward=1
ip netns exec "$router_ns" sysctl -q -w net.ipv6.conf.all.forwarding=1
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.all.rp_filter=0
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.default.rp_filter=0
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.r0.rp_filter=0
ip netns exec "$router_ns" sysctl -q -w net.ipv4.conf.r1.rp_filter=0

"$haproxy_probe" >"$haproxy_candidate_path"
haproxy -c -f "$haproxy_candidate_path" >/dev/null
printf 'invalid directive\n' >>"$haproxy_candidate_path"
if haproxy -c -f "$haproxy_candidate_path" >/dev/null 2>&1; then fail "HAProxy accepted an invalid generated candidate"; fi

ip netns exec "$router_ns" nft add table inet foreign_lab
ip netns exec "$router_ns" nft 'add chain inet foreign_lab marker'

"$nat_probe" >"$candidate_path"
ip netns exec "$router_ns" nft --check --file "$candidate_path"
ip netns exec "$router_ns" nft --file "$candidate_path"

tcp_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:8080,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-tcp.log 2>&1 & echo $!')
udp_pid=$(ip netns exec "$server_ns" sh -c 'socat -T2 UDP-RECVFROM:5353,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-udp.log 2>&1 & echo $!')
tcp6_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP6-LISTEN:8081,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-tcp6.log 2>&1 & echo $!')
dns_pid=$(ip netns exec "$router_ns" sh -c 'socat -T2 UDP-RECVFROM:53,reuseaddr,fork EXEC:/bin/cat >/tmp/egm-dns.log 2>&1 & echo $!')

sleep 0.2

inventory_result=$(ip netns exec "$server_ns" "$inventory_probe")
printf '%s\n' "$inventory_result" | grep -Fq '"name":"s0"' || fail "inventory did not detect server interface"
printf '%s\n' "$inventory_result" | grep -Fq '"port":8080' || fail "inventory did not detect TCP listener"
printf '%s\n' "$inventory_result" | grep -Fq '"port":5353' || fail "inventory did not detect UDP listener"
printf '%s\n' "$inventory_result" | grep -Fq '"name":"nftables","available":true' || fail "inventory did not detect nftables"

route_result=$(ip -n "$client_ns" route get 10.203.2.2)
printf '%s\n' "$route_result" | grep -Fq 'via 10.203.1.1' || fail "client route does not use isolated router"

tcp_result=$(printf 'tcp-ok' | ip netns exec "$client_ns" socat - TCP:10.203.1.1:19080,connect-timeout=2)
[ "$tcp_result" = "tcp-ok" ] || fail "TCP DNAT echo failed"

udp_result=$(printf 'udp-ok' | ip netns exec "$client_ns" timeout 3 socat -T2 - UDP:10.203.1.1:19053)
[ "$udp_result" = "udp-ok" ] || fail "UDP DNAT echo failed"

EGRESS_NAT_TABLE_EXISTS=1 "$nat_probe" >"$candidate_path"
ip netns exec "$router_ns" nft --check --file "$candidate_path"
ip netns exec "$router_ns" nft --file "$candidate_path"

tcp_result=$(printf 'tcp-reload-ok' | ip netns exec "$client_ns" socat - TCP:10.203.1.1:19080,connect-timeout=2)
[ "$tcp_result" = "tcp-reload-ok" ] || fail "TCP DNAT failed after owned-table replacement"
udp_result=$(printf 'udp-reload-ok' | ip netns exec "$client_ns" timeout 3 socat -T2 - UDP:10.203.1.1:19053)
[ "$udp_result" = "udp-reload-ok" ] || fail "UDP DNAT failed after owned-table replacement"

ip -n "$client_ns" address add 10.204.1.2/24 dev c0
if printf 'must-drop' | ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:19080,bind=10.204.1.2 >/dev/null 2>&1; then
    fail "source-CIDR restriction accepted a disallowed source"
fi

ruleset=$(ip netns exec "$router_ns" nft list table ip egm_nat4)
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm_pf_lab_tcp_allow' >/dev/null || fail "TCP counter did not increase"
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm_pf_lab_udp_allow' >/dev/null || fail "UDP counter did not increase"
printf '%s\n' "$ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm_pf_lab_tcp_source_drop' >/dev/null || fail "source restriction drop counter did not increase"
counter_result=$(ip netns exec "$router_ns" "$counter_probe")
printf '%s\n' "$counter_result" | grep -Eq '"forward_id":"lab_tcp","accepted_packets":[1-9][0-9]*' || fail "typed TCP counter reader did not report traffic"
printf '%s\n' "$counter_result" | grep -Eq '"forward_id":"lab_tcp".*"dropped_packets":[1-9][0-9]*' || fail "typed drop counter reader did not report traffic"
printf '%s\n' "$counter_result" | grep -Eq '"forward_id":"lab_udp","accepted_packets":[1-9][0-9]*' || fail "typed UDP counter reader did not report traffic"

ip netns exec "$router_ns" nft delete table ip egm_nat4
if ip netns exec "$router_ns" nft list table ip egm_nat4 >/dev/null 2>&1; then
    fail "owned table still exists after rollback"
fi
ip netns exec "$router_ns" nft list table inet foreign_lab >/dev/null || fail "foreign table was removed during rollback"

ip netns exec "$router_ns" iptables -N FOREIGN_LAB
ip netns exec "$router_ns" iptables -A FOREIGN_LAB -m comment --comment foreign_marker -j RETURN
"$iptables_probe" >"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --test --noflush <"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --noflush <"$iptables_candidate_path"
ip netns exec "$router_ns" env EGRESS_IPTABLES_INSPECT=1 "$iptables_probe" >"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --test --noflush <"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --noflush <"$iptables_candidate_path"
iptables_state=$(ip netns exec "$router_ns" iptables-save)
printf '%s\n' "$iptables_state" | grep -Fq ':FOREIGN_LAB - [0:0]' || fail "iptables adapter removed a foreign chain"
printf '%s\n' "$iptables_state" | grep -Fq 'egm_anchor_prerouting' || fail "iptables adapter did not install owned NAT anchor"
printf '%s\n' "$iptables_state" | grep -Fq 'egm_anchor_forward' || fail "iptables adapter did not install owned filter anchor"
[ "$(printf '%s\n' "$iptables_state" | grep -c 'egm_anchor_prerouting')" -eq 1 ] || fail "iptables adapter duplicated its NAT anchor"
[ "$(printf '%s\n' "$iptables_state" | grep -c 'egm_anchor_forward')" -eq 1 ] || fail "iptables adapter duplicated its filter anchor"

tcp_result=$(printf 'iptables-tcp-ok' | ip netns exec "$client_ns" socat - TCP:10.203.1.1:19080,connect-timeout=2)
[ "$tcp_result" = "iptables-tcp-ok" ] || fail "iptables adapter TCP DNAT echo failed"
udp_result=$(printf 'iptables-udp-ok' | ip netns exec "$client_ns" timeout 3 socat -T2 - UDP:10.203.1.1:19053)
[ "$udp_result" = "iptables-udp-ok" ] || fail "iptables adapter UDP DNAT echo failed"
iptables_counter_result=$(ip netns exec "$router_ns" env EGRESS_COUNTER_ENGINE=iptables "$counter_probe")
printf '%s\n' "$iptables_counter_result" | grep -Eq '"forward_id":"lab_tcp","accepted_packets":[1-9][0-9]*' || fail "iptables typed TCP counter reader did not report traffic"
printf '%s\n' "$iptables_counter_result" | grep -Eq '"forward_id":"lab_udp","accepted_packets":[1-9][0-9]*' || fail "iptables typed UDP counter reader did not report traffic"
ip netns exec "$router_ns" env EGRESS_IPTABLES_ROLLBACK=1 "$iptables_probe" >"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --test --noflush <"$iptables_candidate_path"
ip netns exec "$router_ns" iptables-restore --noflush <"$iptables_candidate_path"
iptables_state=$(ip netns exec "$router_ns" iptables-save)
printf '%s\n' "$iptables_state" | grep -Fq ':FOREIGN_LAB - [0:0]' || fail "iptables rollback removed a foreign chain"
if printf '%s\n' "$iptables_state" | grep -Fq ':EGM_PREROUTING - [0:0]'; then fail "iptables rollback left owned NAT chain"; fi
if printf '%s\n' "$iptables_state" | grep -Fq ':EGM_FORWARD - [0:0]'; then fail "iptables rollback left owned filter chain"; fi

baseline_v4=$(printf 'route-v4-baseline' | ip netns exec "$client_ns" timeout 3 socat - TCP:10.203.2.2:8080,connect-timeout=2)
[ "$baseline_v4" = "route-v4-baseline" ] || fail "route lab IPv4 baseline failed"
baseline_v6=$(printf 'route-v6-baseline' | ip netns exec "$client_ns" timeout 3 socat - TCP6:[2001:db8:203:2::2]:8081,connect-timeout=2)
[ "$baseline_v6" = "route-v6-baseline" ] || fail "route lab IPv6 baseline failed"
baseline_dns=$(printf 'route-dns-baseline' | ip netns exec "$client_ns" timeout 3 socat -T2 - UDP:10.203.1.1:53)
[ "$baseline_dns" = "route-dns-baseline" ] || fail "route lab local DNS baseline failed"

"$routing_probe" "$route_candidate_dir"
route_tun=$(tr -d '\r\n' <"$route_candidate_dir/tun-name")
route_table=$(tr -d '\r\n' <"$route_candidate_dir/table")
route_priority=$(tr -d '\r\n' <"$route_candidate_dir/priority")
ip -n "$router_ns" link add "$route_tun" type dummy
ip -n "$router_ns" link set "$route_tun" up
ip netns exec "$router_ns" nft --check --file "$route_candidate_dir/routing.nft"
ip netns exec "$router_ns" nft --file "$route_candidate_dir/routing.nft"
ip netns exec "$router_ns" ip -4 -batch "$route_candidate_dir/routing-v4.batch"
ip netns exec "$router_ns" ip -6 -batch "$route_candidate_dir/routing-v6.batch"
ip netns exec "$router_ns" "$routing_probe" --verify-runtime "$route_candidate_dir"
ip netns exec "$router_ns" ip -j -4 rule show priority "$route_priority" | grep -Fq '"protocol":"242"' || fail "owned IPv4 route rule marker missing"
ip netns exec "$router_ns" ip -j -6 rule show priority "$route_priority" | grep -Fq '"protocol":"242"' || fail "owned IPv6 route rule marker missing"

ip -n "$router_ns" link delete "$route_tun"
ip netns exec "$router_ns" nft delete table inet egm_egress
ip netns exec "$router_ns" ip -4 rule delete priority "$route_priority" table "$route_table" protocol 242
ip netns exec "$router_ns" ip -6 rule delete priority "$route_priority" table "$route_table" protocol 242
ip netns exec "$router_ns" ip -4 route flush table "$route_table"
ip netns exec "$router_ns" ip -6 route flush table "$route_table"
ip netns exec "$router_ns" "$routing_probe" --reconcile "$route_candidate_dir"
ip netns exec "$router_ns" "$routing_probe" --verify-runtime "$route_candidate_dir"
printf 'PASS: cold volatile-state loss reconciles once and repeated recovery is idempotent\n'

ip -n "$router_ns" link delete "$route_tun"
endpoint_result=$(printf 'endpoint-main-ok' | ip netns exec "$router_ns" timeout 3 socat - TCP:10.203.2.3:8080,connect-timeout=2)
[ "$endpoint_result" = "endpoint-main-ok" ] || fail "outbound endpoint main-table path was captured"

capture_path="$route_candidate_dir/direct-leak.pcap"
ip netns exec "$router_ns" timeout 8 tcpdump -U -n -i r1 -w "$capture_path" 'src host 10.203.1.2 or src host 2001:db8:203:1::2' >/dev/null 2>&1 &
capture_pid=$!
sleep 0.3
if printf 'must-not-leak-v4' | ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.2.2:8080,connect-timeout=1 >/dev/null 2>&1; then fail "killed outbound leaked IPv4 traffic to main routing"; fi
if printf 'must-not-leak-v6' | ip netns exec "$client_ns" timeout 2 socat - TCP6:[2001:db8:203:2::2]:8081,connect-timeout=1 >/dev/null 2>&1; then fail "blocked IPv6 traffic bypassed the selected route"; fi
dns_leak=$(printf 'must-not-leak-dns' | ip netns exec "$client_ns" timeout 2 socat -T1 - UDP:10.203.1.1:53 2>/dev/null || true)
[ "$dns_leak" != "must-not-leak-dns" ] || fail "follow-outbound DNS reached the host resolver"
if wait "$capture_pid"; then fail "client packet escaped through the direct egress interface"; fi
captured_packets=$(ip netns exec "$router_ns" tcpdump -n -r "$capture_path" 2>/dev/null | wc -l)
[ "$captured_packets" -eq 0 ] || fail "packet capture observed direct client egress"
route_ruleset=$(ip netns exec "$router_ns" nft list table inet egm_egress)
printf '%s\n' "$route_ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm_lab_route_ipv4_kill' >/dev/null || fail "IPv4 kill-switch counter did not increase"
printf '%s\n' "$route_ruleset" | grep -E 'counter packets [1-9][0-9]* bytes [1-9][0-9]*.*egm_lab_route_dns_input' >/dev/null || fail "DNS input leak counter did not increase"

ip netns exec "$router_ns" "$routing_probe" --bypass "$route_candidate_dir"
if ip netns exec "$router_ns" nft list table inet egm_egress >/dev/null 2>&1; then fail "emergency bypass left owned interception table"; fi
if ip netns exec "$router_ns" ip -j -4 rule show priority "$route_priority" | grep -Fq '"protocol":"242"'; then fail "emergency bypass left owned IPv4 rule"; fi
if ip netns exec "$router_ns" ip -j -6 rule show priority "$route_priority" | grep -Fq '"protocol":"242"'; then fail "emergency bypass left owned IPv6 rule"; fi
ip netns exec "$router_ns" nft list table inet foreign_lab >/dev/null || fail "emergency bypass removed a foreign nftables table"
[ -s "$route_candidate_dir/routing-state.json" ] || fail "emergency bypass removed persistent routing state"
printf 'PASS: killed outbound blocks leaks; emergency bypass is idempotent and preserves desired and foreign state\n'

wireguard_name=$(tr -d '\r\n' <"$interface_candidate_dir/wireguard-name")
wireguard_id=$(tr -d '\r\n' <"$interface_candidate_dir/wireguard-id")
mkdir -m 700 "$wireguard_candidate_dir"
umask 077
wg genkey >"$wireguard_candidate_dir/router.key"
wg pubkey <"$wireguard_candidate_dir/router.key" >"$wireguard_candidate_dir/router.pub"
wg genkey >"$wireguard_candidate_dir/server.key"
wg pubkey <"$wireguard_candidate_dir/server.key" >"$wireguard_candidate_dir/server.pub"
router_public=$(tr -d '\r\n' <"$wireguard_candidate_dir/router.pub")
server_public=$(tr -d '\r\n' <"$wireguard_candidate_dir/server.pub")
wireguard_transport_emulated=false
if uname -r | grep -qi microsoft; then
    wireguard_transport_emulated=true
    ip netns exec "$router_ns" ip link add dev "$wireguard_name" type wireguard
    ip netns exec "$router_ns" wg set "$wireguard_name" private-key "$wireguard_candidate_dir/router.key" peer "$server_public" allowed-ips 0.0.0.0/0 endpoint 10.203.2.2:51820 persistent-keepalive 1
    ip netns exec "$router_ns" ip -json -details link show dev "$wireguard_name" | grep -Fq '"info_kind":"wireguard"' || fail "WireGuard kernel lifecycle kind verification failed"
    ip netns exec "$router_ns" wg show "$wireguard_name" peers | grep -Fq "$server_public" || fail "WireGuard kernel peer verification failed"
    ip netns exec "$router_ns" ip link delete dev "$wireguard_name"
    ip link add "$wireguard_name" type veth peer name wgsrv0
    ip link set "$wireguard_name" netns "$router_ns"
    ip link set wgsrv0 netns "$server_ns"
else
    ip netns exec "$router_ns" ip link add dev "$wireguard_name" type wireguard
    ip netns exec "$server_ns" ip link add dev wgsrv0 type wireguard
    ip netns exec "$router_ns" wg set "$wireguard_name" private-key "$wireguard_candidate_dir/router.key" peer "$server_public" allowed-ips 0.0.0.0/0 endpoint 10.203.2.2:51820 persistent-keepalive 1
    ip netns exec "$server_ns" wg set wgsrv0 listen-port 51820 private-key "$wireguard_candidate_dir/server.key" peer "$router_public" allowed-ips 10.210.0.1/32,10.203.1.0/24
fi
ip -n "$router_ns" address add 10.210.0.1/30 dev "$wireguard_name"
ip -n "$server_ns" address add 10.210.0.2/30 dev wgsrv0
ip -n "$router_ns" link set "$wireguard_name" up
ip -n "$server_ns" link set wgsrv0 up
if [ "$wireguard_transport_emulated" = true ]; then
    ip -n "$server_ns" route add 10.203.1.0/24 via 10.210.0.1 dev wgsrv0
else
    ip -n "$server_ns" route add 10.203.1.0/24 dev wgsrv0
fi
ip netns exec "$router_ns" timeout 5 ping -c 1 -W 3 10.210.0.2 >/dev/null || fail "WireGuard native interface baseline failed"
ip netns exec "$router_ns" env EGRESS_ROUTE_ADAPTER=interface EGRESS_ROUTE_TYPE=wireguard EGRESS_ROUTE_INTERFACE="$wireguard_name" EGRESS_ROUTE_OUTBOUND_ID="$wireguard_id" "$routing_probe" "$wireguard_candidate_dir"
wireguard_table=$(tr -d '\r\n' <"$wireguard_candidate_dir/table")
wireguard_priority=$(tr -d '\r\n' <"$wireguard_candidate_dir/priority")
ip netns exec "$router_ns" nft --check --file "$wireguard_candidate_dir/routing.nft"
ip netns exec "$router_ns" nft --file "$wireguard_candidate_dir/routing.nft"
ip netns exec "$router_ns" ip -4 -batch "$wireguard_candidate_dir/routing-v4.batch"
ip netns exec "$router_ns" ip -6 -batch "$wireguard_candidate_dir/routing-v6.batch"
ip netns exec "$router_ns" "$routing_probe" --verify-runtime "$wireguard_candidate_dir"
if ! ip netns exec "$client_ns" timeout 5 ping -c 1 -W 3 10.210.0.2 >/dev/null; then
    ip netns exec "$router_ns" wg show >&2 || true
    ip netns exec "$router_ns" ip -4 rule show >&2 || true
    ip netns exec "$router_ns" ip -4 route show table "$wireguard_table" >&2 || true
    ip netns exec "$router_ns" ip route get 10.210.0.2 from 10.203.1.2 iif r0 >&2 || true
    ip netns exec "$router_ns" nft list table inet egm_egress >&2 || true
    ip netns exec "$router_ns" ip -s link show dev "$wireguard_name" >&2 || true
    ip netns exec "$server_ns" wg show >&2 || true
    ip netns exec "$server_ns" ip -4 route show >&2 || true
    ip netns exec "$server_ns" ip -s link show dev wgsrv0 >&2 || true
    fail "WireGuard routed client traffic did not use the native interface"
fi
if [ "$wireguard_transport_emulated" = false ]; then
    wireguard_handshake=$(ip netns exec "$router_ns" wg show "$wireguard_name" latest-handshakes | awk '{print $2}')
    [ "${wireguard_handshake:-0}" -gt 0 ] || fail "WireGuard handshake evidence was not observed"
fi
ip -n "$router_ns" link delete dev "$wireguard_name"
wireguard_capture="$wireguard_candidate_dir/direct-leak.pcap"
ip netns exec "$router_ns" timeout 5 tcpdump -U -n -i r1 -w "$wireguard_capture" 'src host 10.203.1.2' >/dev/null 2>&1 &
wireguard_capture_pid=$!
sleep 0.3
if printf 'wireguard-must-not-leak' | ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.2.3:8080,connect-timeout=1 >/dev/null 2>&1; then fail "failed WireGuard interface leaked client traffic"; fi
if wait "$wireguard_capture_pid"; then fail "WireGuard failure capture ended unexpectedly"; fi
wireguard_packets=$(ip netns exec "$router_ns" tcpdump -n -r "$wireguard_capture" 2>/dev/null | wc -l)
[ "$wireguard_packets" -eq 0 ] || fail "failed WireGuard interface leaked packets to direct egress"
ip netns exec "$router_ns" ip -4 rule delete priority "$wireguard_priority" table "$wireguard_table" protocol 242
ip netns exec "$router_ns" ip -6 rule delete priority "$wireguard_priority" table "$wireguard_table" protocol 242
ip netns exec "$router_ns" ip -4 route flush table "$wireguard_table" proto 242
ip netns exec "$router_ns" ip -6 route flush table "$wireguard_table" proto 242
ip netns exec "$router_ns" nft delete table inet egm_egress
ip -n "$server_ns" link delete dev wgsrv0 2>/dev/null || true
if [ "$wireguard_transport_emulated" = true ]; then
    printf 'PASS: kernel WireGuard lifecycle plus WSL-safe route transport emulation fail closed after interface loss\n'
else
    printf 'PASS: native WireGuard routes client traffic and fails closed after interface loss\n'
fi

openvpn_name=$(tr -d '\r\n' <"$interface_candidate_dir/openvpn-name")
openvpn_id=$(tr -d '\r\n' <"$interface_candidate_dir/openvpn-id")
mkdir -m 700 "$openvpn_runtime_dir" "$openvpn_candidate_dir"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$openvpn_runtime_dir/ca.key" -out "$openvpn_runtime_dir/ca.crt" -subj /CN=egress-manager-test-ca -days 1 -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$openvpn_runtime_dir/server.key" -out "$openvpn_runtime_dir/server.csr" -subj /CN=egress-manager-test-server >/dev/null 2>&1
printf 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:egress-manager-test-server\n' >"$openvpn_runtime_dir/server.ext"
openssl x509 -req -in "$openvpn_runtime_dir/server.csr" -CA "$openvpn_runtime_dir/ca.crt" -CAkey "$openvpn_runtime_dir/ca.key" -CAcreateserial -out "$openvpn_runtime_dir/server.crt" -days 1 -extfile "$openvpn_runtime_dir/server.ext" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$openvpn_runtime_dir/client.key" -out "$openvpn_runtime_dir/client.csr" -subj /CN=egress-manager-test-client >/dev/null 2>&1
printf 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=clientAuth\n' >"$openvpn_runtime_dir/client.ext"
openssl x509 -req -in "$openvpn_runtime_dir/client.csr" -CA "$openvpn_runtime_dir/ca.crt" -CAkey "$openvpn_runtime_dir/ca.key" -CAserial "$openvpn_runtime_dir/ca.srl" -out "$openvpn_runtime_dir/client.crt" -days 1 -extfile "$openvpn_runtime_dir/client.ext" >/dev/null 2>&1
mkdir -m 700 "$openvpn_runtime_dir/ccd"
printf 'iroute 10.203.1.0 255.255.255.0\n' >"$openvpn_runtime_dir/ccd/egress-manager-test-client"
cat >"$openvpn_runtime_dir/server.conf" <<EOF
local 10.203.2.2
port 1194
proto udp
dev tun
topology subnet
server 10.220.0.0 255.255.255.0
route 10.203.1.0 255.255.255.0
client-config-dir $openvpn_runtime_dir/ccd
ca $openvpn_runtime_dir/ca.crt
cert $openvpn_runtime_dir/server.crt
key $openvpn_runtime_dir/server.key
dh none
data-ciphers AES-256-GCM
keepalive 2 10
persist-key
persist-tun
verb 1
EOF
{
    printf 'client\ndev %s\ndev-type tun\nproto udp\nremote 10.203.2.2 1194\nremote-cert-tls server\nnobind\nroute-nopull\nauth-nocache\npersist-key\npersist-tun\ndata-ciphers AES-256-GCM\nverb 1\n<ca>\n' "$openvpn_name"
    cat "$openvpn_runtime_dir/ca.crt"
    printf '</ca>\n<cert>\n'
    cat "$openvpn_runtime_dir/client.crt"
    printf '</cert>\n<key>\n'
    cat "$openvpn_runtime_dir/client.key"
    printf '</key>\n'
} >"$openvpn_runtime_dir/client.conf"
if ! timeout 8 openvpn --config "$openvpn_runtime_dir/client.conf" --show-tls >"$openvpn_runtime_dir/validation.log" 2>&1; then
    cat "$openvpn_runtime_dir/validation.log" >&2 || true
    fail "OpenVPN generated client profile failed native validation"
fi
ip netns exec "$server_ns" openvpn --config "$openvpn_runtime_dir/server.conf" >"$openvpn_runtime_dir/server.log" 2>&1 &
openvpn_server_pid=$!
sleep 0.5
ip netns exec "$router_ns" openvpn --config "$openvpn_runtime_dir/client.conf" >"$openvpn_runtime_dir/client.log" 2>&1 &
openvpn_client_pid=$!
openvpn_wait=0
while ! ip -n "$router_ns" link show dev "$openvpn_name" >/dev/null 2>&1; do
    openvpn_wait=$((openvpn_wait + 1))
    if [ "$openvpn_wait" -ge 50 ]; then
        cat "$openvpn_runtime_dir/server.log" >&2 || true
        cat "$openvpn_runtime_dir/client.log" >&2 || true
        fail "OpenVPN client interface did not become ready"
    fi
    sleep 0.2
done
ip netns exec "$router_ns" env EGRESS_ROUTE_ADAPTER=interface EGRESS_ROUTE_TYPE=openvpn EGRESS_ROUTE_INTERFACE="$openvpn_name" EGRESS_ROUTE_OUTBOUND_ID="$openvpn_id" "$routing_probe" "$openvpn_candidate_dir"
openvpn_table=$(tr -d '\r\n' <"$openvpn_candidate_dir/table")
openvpn_priority=$(tr -d '\r\n' <"$openvpn_candidate_dir/priority")
ip netns exec "$router_ns" nft --check --file "$openvpn_candidate_dir/routing.nft"
ip netns exec "$router_ns" nft --file "$openvpn_candidate_dir/routing.nft"
ip netns exec "$router_ns" ip -4 -batch "$openvpn_candidate_dir/routing-v4.batch"
ip netns exec "$router_ns" ip -6 -batch "$openvpn_candidate_dir/routing-v6.batch"
ip netns exec "$router_ns" "$routing_probe" --verify-runtime "$openvpn_candidate_dir"
if ! ip netns exec "$client_ns" timeout 5 ping -c 1 -W 3 10.220.0.1 >/dev/null; then
    ip netns exec "$router_ns" ip -4 rule show >&2 || true
    ip netns exec "$router_ns" ip -4 route show table "$openvpn_table" >&2 || true
    ip netns exec "$router_ns" ip route get 10.220.0.1 from 10.203.1.2 iif r0 >&2 || true
    ip netns exec "$router_ns" nft list table inet egm_egress >&2 || true
    ip netns exec "$router_ns" ip -s link show dev "$openvpn_name" >&2 || true
    ip netns exec "$server_ns" ip -4 route show >&2 || true
    cat "$openvpn_runtime_dir/server.log" >&2 || true
    cat "$openvpn_runtime_dir/client.log" >&2 || true
    fail "OpenVPN routed client traffic did not use the native interface"
fi
kill "$openvpn_client_pid"
wait "$openvpn_client_pid" 2>/dev/null || true
openvpn_client_pid=""
openvpn_capture="$openvpn_candidate_dir/direct-leak.pcap"
ip netns exec "$router_ns" timeout 5 tcpdump -U -n -i r1 -w "$openvpn_capture" 'src host 10.203.1.2' >/dev/null 2>&1 &
openvpn_capture_pid=$!
sleep 0.3
if printf 'openvpn-must-not-leak' | ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.2.3:8080,connect-timeout=1 >/dev/null 2>&1; then fail "failed OpenVPN interface leaked client traffic"; fi
if wait "$openvpn_capture_pid"; then fail "OpenVPN failure capture ended unexpectedly"; fi
openvpn_packets=$(ip netns exec "$router_ns" tcpdump -n -r "$openvpn_capture" 2>/dev/null | wc -l)
[ "$openvpn_packets" -eq 0 ] || fail "failed OpenVPN interface leaked packets to direct egress"
ip netns exec "$router_ns" ip -4 rule delete priority "$openvpn_priority" table "$openvpn_table" protocol 242
ip netns exec "$router_ns" ip -6 rule delete priority "$openvpn_priority" table "$openvpn_table" protocol 242
ip netns exec "$router_ns" ip -4 route flush table "$openvpn_table" proto 242
ip netns exec "$router_ns" ip -6 route flush table "$openvpn_table" proto 242
ip netns exec "$router_ns" nft delete table inet egm_egress
kill "$openvpn_server_pid"
wait "$openvpn_server_pid" 2>/dev/null || true
openvpn_server_pid=""
printf 'PASS: native OpenVPN routes client traffic and fails closed after interface loss\n'

haproxy_primary_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:18081,reuseaddr,fork SYSTEM:"printf primary" >/tmp/egm-haproxy-primary.log 2>&1 & echo $!')
haproxy_secondary_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:18082,reuseaddr,fork SYSTEM:"printf secondary" >/tmp/egm-haproxy-secondary.log 2>&1 & echo $!')
haproxy_backup_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:18083,reuseaddr,fork SYSTEM:"printf backup" >/tmp/egm-haproxy-backup.log 2>&1 & echo $!')
sleep 0.3
haproxy_apply=$(ip netns exec "$router_ns" env EGRESS_HAPROXY_EXECUTE=1 "$haproxy_probe")
printf '%s\n' "$haproxy_apply" | grep -Fq '"state":"COMMITTED"' || fail "HAProxy transactional initial apply did not commit"

distribution=""
attempt=0
while [ "$attempt" -lt 30 ]; do
    response=$(ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:18080 2>/dev/null || true)
    distribution="$distribution $response"
    if printf '%s\n' "$distribution" | grep -Fq primary && printf '%s\n' "$distribution" | grep -Fq secondary; then break; fi
    attempt=$((attempt + 1))
    sleep 0.1
done
printf '%s\n' "$distribution" | grep -Fq primary || fail "HAProxy pool did not select primary backend"
printf '%s\n' "$distribution" | grep -Fq secondary || fail "HAProxy pool did not distribute to secondary backend"

kill "$haproxy_primary_pid" "$haproxy_secondary_pid"
haproxy_primary_pid=""
haproxy_secondary_pid=""
attempt=0
failover=""
while [ "$attempt" -lt 50 ]; do
    failover=$(ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:18080 2>/dev/null || true)
    [ "$failover" = "backup" ] && break
    attempt=$((attempt + 1))
    sleep 0.2
done
[ "$failover" = "backup" ] || fail "HAProxy did not fail over to backup backend"

haproxy_primary_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:18081,reuseaddr,fork SYSTEM:"printf primary" >/tmp/egm-haproxy-primary.log 2>&1 & echo $!')
attempt=0
recovered=""
while [ "$attempt" -lt 50 ]; do
    recovered=$(ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:18080 2>/dev/null || true)
    [ "$recovered" = "primary" ] && break
    attempt=$((attempt + 1))
    sleep 0.2
done
[ "$recovered" = "primary" ] || fail "HAProxy primary backend did not recover"

old_haproxy_pid=$(cat /tmp/egm-haproxy.pid)
haproxy_reload=$(ip netns exec "$router_ns" env EGRESS_HAPROXY_EXECUTE=1 "$haproxy_probe")
printf '%s\n' "$haproxy_reload" | grep -Fq '"state":"COMMITTED"' || fail "HAProxy graceful reload did not commit"
new_haproxy_pid=$(cat /tmp/egm-haproxy.pid)
[ "$old_haproxy_pid" != "$new_haproxy_pid" ] || fail "HAProxy graceful reload did not replace master PID"
attempt=0
reload_response=""
while [ "$attempt" -lt 30 ]; do
    reload_response=$(ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:18080 2>/dev/null || true)
    case "$reload_response" in primary|backup) break ;; esac
    attempt=$((attempt + 1))
    sleep 0.1
done
case "$reload_response" in primary|backup) ;; *) fail "HAProxy traffic failed after graceful reload" ;; esac
runtime_snapshot=$(ip netns exec "$router_ns" env EGRESS_HAPROXY_STATS=1 "$haproxy_probe")
printf '%s\n' "$runtime_snapshot" | grep -Fq '"frontend_id":"lab_proxy"' || fail "HAProxy Runtime API omitted owned frontend statistics"
printf '%s\n' "$runtime_snapshot" | grep -Fq '"backend_id":"lab_primary"' || fail "HAProxy Runtime API omitted backend statistics"

printf 'PASS: isolated NAT engines, HAProxy distribution/failover/recovery/reload, counters, rollback, and foreign preservation\n'
