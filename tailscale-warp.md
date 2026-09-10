# Tailscale + Cloudflare WARP on macOS

## Summary

Tailscale and WARP can run together on this Mac. Excluding the tailnet and its subnet routes from WARP was insufficient: Tailscale's public relay/control connections also needed to bypass WARP. Adding the current Singapore DERP relay IPv4 addresses and `controlplane.tailscale.com` restored simultaneous connectivity.

Tested on September 10, 2026:

| Component | Version / configuration |
| --- | --- |
| macOS | 26.6.2, build 25G83 |
| Tailscale | 1.102.3, App Store network extension (`io.tailscale.ipn.macos`) |
| WARP | 2026.7.1376.0, MASQUE |
| WARP mode | `warp` — tunnel plus DNS proxy |
| Split tunnels | Exclude mode |
| Tailscale DNS | Enabled |
| Tailscale exit node | None |

## Symptom and root cause

With WARP disconnected, a Tailscale peer replied through DERP Singapore. Connecting WARP made the same peer time out, while Cloudflare's trace endpoint reported `warp=on`. The peer's route still pointed to Tailscale's tunnel interface.

The existing WARP exclusions already included:

- `100.64.0.0/10`: Tailscale IPv4 addresses.
- `fc00::/7`: covers Tailscale's IPv6 range.
- `10.0.0.0/8`, covering the private subnet routes used in this test.

These cover the **inner destination addresses**, but a relayed Tailscale connection also needs an **outer connection to a public DERP server**. Those public addresses were still routed through WARP. Tailscale logs showed connection refusals to public bootstrap/relay servers and failures reaching its control plane while WARP was enabled.

The successful exclusion test identifies public transport reachability as the configuration gap. It does not establish the precise internal WARP/macOS filtering mechanism. The relay and control exclusions were added together, so their individual necessity was not tested separately.

## Fix

Keep the existing tailnet/subnet exclusions. Add the following exclusions, then connect WARP:

```sh
warp-cli tunnel ip add 172.237.72.8
warp-cli tunnel ip add 172.237.66.30
warp-cli tunnel ip add 172.237.72.79
warp-cli tunnel ip add 172.237.72.43
warp-cli tunnel host add controlplane.tailscale.com
warp-cli connect
```

These were the four Singapore relay addresses returned by the local Tailscale DERP map at test time:

| Relay | IPv4 |
| --- | --- |
| `derp3f.tailscale.com` | `172.237.72.8` |
| `derp3h.tailscale.com` | `172.237.66.30` |
| `derp3g.tailscale.com` | `172.237.72.79` |
| `derp3e.tailscale.com` | `172.237.72.43` |

The local CLI labels these mutation commands as consumer-only. For an organization-managed client, configure the corresponding exclusions in the applicable device profile.

The final working configuration retained `warp` mode and Tailscale DNS. Switching WARP to `tunnel_only` was tested separately and **did not fix the peer timeout**; DNS contention alone did not explain this failure.

## Validation

Replace the example variables with an online peer, its full MagicDNS name, and an address in an advertised subnet:

```sh
TAILSCALE_PEER_IP='100.x.y.z'
TAILSCALE_PEER_NAME='peer.your-tailnet.ts.net'
TAILSCALE_SUBNET_IP='YOUR_SUBNET_HOST_IP'

warp-cli status
tailscale ping --timeout=3s --c=2 "$TAILSCALE_PEER_IP"
dscacheutil -q host -a name "$TAILSCALE_PEER_NAME"
curl -sS --max-time 6 https://www.cloudflare.com/cdn-cgi/trace | rg 'warp=|colo='
route -n get "$TAILSCALE_SUBNET_IP"
```

Observed after the fix:

- WARP: `Connected`, `Network: healthy`.
- Internet trace: `warp=on`, `colo=SIN`.
- Tailscale peer: repeated replies through `DERP(sin)`; observed latencies ranged from 6 to 125 ms.
- MagicDNS: the peer name resolved to its expected Tailscale IPv4 address.
- Subnet route: the tested private subnet still used Tailscale's `utun4` interface. Interface numbers are machine/session-specific.
- The user confirmed the setup worked; both connections were left running.

Direct peer connectivity was not established in these checks. Subnet routing was inspected, but an application inside the subnet was not tested. Other relay regions, IPv6 transport, reboot persistence, and long-term stability were not validated.

## Refresh relay exclusions

DERP addresses can change, and a different network or location may select another region. The four addresses above are a tested snapshot, not a permanent global list.

Check the current preferred region and inspect its relay map:

```sh
tailscale netcheck
tailscale debug derp-map | python3 -c '
import json, sys
data = json.load(sys.stdin)
for region in data["Regions"].values():
    if region.get("RegionCode") == "sin":
        for node in region.get("Nodes", []):
            print(node["HostName"], node.get("IPv4", ""), node.get("IPv6", ""))
'
```

Change `sin` to the region you need. Review the returned addresses, update WARP exclusions, and rerun both the Tailscale peer test and the WARP trace test. `tailscale debug` is a diagnostic interface and may change between versions.

## Rollback

Remove only the entries added by this fix; preserve the original tailnet/subnet exclusions:

```sh
warp-cli tunnel ip remove 172.237.72.8
warp-cli tunnel ip remove 172.237.66.30
warp-cli tunnel ip remove 172.237.72.79
warp-cli tunnel ip remove 172.237.72.43
warp-cli tunnel host remove controlplane.tailscale.com
```

If this restores the conflict, `warp-cli disconnect` returns to the tested Tailscale-only baseline. Changing either VPN can briefly interrupt active network sessions.

## References

- [Cloudflare: coexistence with other VPNs](https://developers.cloudflare.com/cloudflare-one/team-and-resources/devices/cloudflare-one-client/deployment/vpn/) — excludes both private destination ranges and the other VPN's public server connections; also describes DNS ownership.
- [Tailscale: interoperability with other software](https://tailscale.com/docs/reference/interoperability) — compatibility depends on the operating system, client versions, and configuration.
- [Tailscale: macOS variants](https://tailscale.com/docs/concepts/macos-variants) — distinguishes the App Store, standalone, and open-source daemon variants. No variant migration was needed for this tested fix.
