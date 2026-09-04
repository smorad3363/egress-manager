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

for command_name in ip nft iptables iptables-save iptables-restore haproxy sing-box socat sysctl timeout grep awk; do
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
haproxy_primary_pid=""
haproxy_secondary_pid=""
haproxy_backup_pid=""
candidate_path="/tmp/egm-nat-$suffix.nft"
iptables_candidate_path="/tmp/egm-nat-$suffix.iptables"
haproxy_candidate_path="/tmp/egm-haproxy-$suffix.cfg"

namespace_exists() {
    ip netns list | awk '{print $1}' | grep -Fxq "$1"
}

cleanup() {
	rm -f "$candidate_path" "$iptables_candidate_path" "$haproxy_candidate_path"
    if [ -f /tmp/egm-haproxy.pid ]; then
        haproxy_pid=$(cat /tmp/egm-haproxy.pid 2>/dev/null || true)
        if [ -n "$haproxy_pid" ]; then kill "$haproxy_pid" 2>/dev/null || true; fi
    fi
    rm -f /tmp/egm-haproxy.pid /tmp/egm-haproxy-runtime.sock /tmp/egm-haproxy-managed.cfg
    for process_id in "$tcp_pid" "$udp_pid" "$haproxy_primary_pid" "$haproxy_secondary_pid" "$haproxy_backup_pid"; do
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
while [ "$attempt" -lt 30 ]; do
    failover=$(ip netns exec "$client_ns" timeout 2 socat - TCP:10.203.1.1:18080 2>/dev/null || true)
    [ "$failover" = "backup" ] && break
    attempt=$((attempt + 1))
    sleep 0.2
done
[ "$failover" = "backup" ] || fail "HAProxy did not fail over to backup backend"

haproxy_primary_pid=$(ip netns exec "$server_ns" sh -c 'socat TCP-LISTEN:18081,reuseaddr,fork SYSTEM:"printf primary" >/tmp/egm-haproxy-primary.log 2>&1 & echo $!')
attempt=0
recovered=""
while [ "$attempt" -lt 30 ]; do
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
