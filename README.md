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

## Environment Variables

- `CONFIG_PATH` - Path to config file (default: `config.yaml`)
- `LISTEN_ADDR` - Address to listen on (default: `:8080`)
