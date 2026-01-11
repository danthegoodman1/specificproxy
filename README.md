# specificproxy

HTTP proxy that allows specifying an egress IP address per request. Useful for machines with multiple IPv6 addresses.

## Configuration

Create `config.yaml` with allowed network interfaces:

```yaml
allowed_interfaces:
  - eth0
  - eth1
```

## Usage

```bash
# Start the server
./specificproxy

# List available egress IPs
curl http://localhost:8080/ips
# {
#   "ips": [
#     {"interface": "eth0", "ip": "192.168.1.10", "version": 4},
#     {"interface": "eth0", "ip": "2a01:4ff:1f0:11f8::1", "version": 6}
#   ]
# }

# Proxy request with specific egress IP
curl -x http://localhost:8080 --proxy-header "X-Egress-IP: 2a01:4ff:1f0:11f8::1" https://icanhazip.com

# Proxy request with random egress IP (omit header)
curl -x http://localhost:8080 https://icanhazip.com
```

## Rate Limiting

Optional per-request rate limiting via `X-Rate-Limit` header. Rate limits are keyed per egress IP and resource.

```bash
# Rate limit: 10 requests per 60 seconds, keyed by domain
curl -x http://localhost:8080 \
  --proxy-header "X-Egress-IP: 2a01:4ff:1f0:11f8::1" \
  --proxy-header 'X-Rate-Limit: {"method":"token_bucket","rate":10,"period":60,"resource":{"kind":"domain"}}' \
  https://example.com
```

Header format:
```json
{
  "method": "token_bucket",
  "rate": 10,
  "period": 60,
  "ttl": 300,
  "resource": {"kind": "domain"}
}
```

Options:
- `method`: `token_bucket` or `fixed_window`
- `rate`: requests allowed per period
- `period`: period in seconds
- `ttl`: limiter TTL in seconds (default 300), resets on each use
- `resource.kind`: `domain` or `domain_path`

When rate limited by the proxy, the response includes `X-RateLimit-Source: specificproxy` header to distinguish from destination rate limits.

## Environment Variables

- `CONFIG_PATH` - Path to config file (default: `config.yaml`)
- `LISTEN_ADDR` - Address to listen on (default: `:8080`)
