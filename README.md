# xDSnap

xDSnap is a CLI tool to capture and organize Envoy configuration snapshots from Consul Connect sidecars running on Nomad for streamlined service mesh diagnostics.

## Table of Contents

- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Usage](#usage)
- [Examples](#examples)
- [Configuration](#configuration)
- [Feature Requests](#-feature-requests)

---

## Features

- **Capture Envoy Snapshots**: Fetch data from Envoy admin endpoints, sidecar logs, and application logs.
- **Consul Service Discovery**: Automatically discover Consul Connect allocations via Consul catalog.
- **Optional TCPDump**: Capture network traffic via `nomad alloc exec` (requires tcpdump in sidecar image).
- **Data Archival**: Save collected data as `.tar.gz` files for easier storage and transfer.

---

## Prerequisites

- Nomad cluster with Consul Connect service mesh and Envoy sidecars configured.
- Go 1.18+ installed for building the project from source.
- Network access to Nomad and Consul APIs.
- Nomad ACL token with `alloc:read` and `alloc:exec` capabilities (if ACLs enabled).
- Consul ACL token with `service:read` capability (if ACLs enabled).

---

## Installation

### From Source

```bash
git clone https://github.com/markcampv/xDSnap.git
cd xDSnap
go build -o xdsnap ./cmd/
```

### Install to GOPATH

```bash
go install ./cmd/
```

This installs `xdsnap` to `$GOPATH/bin` (usually `~/go/bin`).

---

## Usage

The main command is `capture`, which collects snapshots from Envoy sidecars in Consul Connect allocations.

### Basic Command

```bash
# Capture all Consul Connect allocations
xdsnap capture

# Capture a specific allocation
xdsnap capture --alloc <allocation-id>

# Capture by Consul service name
xdsnap capture --service <service-name>
```

### Flags

| Flag | Description |
|------|-------------|
| `--alloc` | Allocation ID (optional; if omitted, discovers all Connect allocations) |
| `--task` | Task name for application logs (auto-detected if not specified) |
| `--service` | Filter allocations by Consul service name |
| `-n`, `--namespace` | Nomad namespace (optional) |
| `--sleep` | Interval between captures in seconds (default: 5, minimum: 5) |
| `--duration` | Total capture duration in seconds (default: 60) |
| `--repeat` | Number of snapshot repetitions (takes precedence over duration) |
| `--enable-trace` | Set Envoy log level to trace during capture (auto-reverts to info) |
| `--tcpdump` | Enable tcpdump capture (requires tcpdump in sidecar image) |
| `--output-dir` | Directory to save snapshots (default: current directory) |
| `--endpoints` | Envoy admin endpoints to capture (default: `/stats`, `/config_dump`, `/listeners`, `/clusters`, `/certs`) |

---

## Examples

### Capture all Consul Connect allocations

```bash
xdsnap capture
```

### Capture a specific service

```bash
xdsnap capture --service web --duration 60
```

### Capture a specific allocation

```bash
xdsnap capture --alloc abc123de-f456-7890-abcd-ef1234567890
```

### Enable verbose Envoy logs (trace) during capture

```bash
xdsnap capture --service api --duration 120 --enable-trace
```

### Run exactly 3 snapshots

```bash
xdsnap capture --service dashboard --repeat 3
```

### Capture network traffic with tcpdump

```bash
xdsnap capture --service dashboard --tcpdump
```

> **Note**: tcpdump requires the binary to be available in the sidecar image. If not available, the capture will skip tcpdump with a warning.

### Run trace logging and tcpdump together

```bash
xdsnap capture --service dashboard --enable-trace --tcpdump --duration 30
```

### Filter by namespace

```bash
xdsnap capture --namespace production --service api
```

---

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `NOMAD_ADDR` | Nomad API address | `http://127.0.0.1:4646` |
| `NOMAD_TOKEN` | Nomad ACL token | (none) |
| `NOMAD_NAMESPACE` | Default Nomad namespace | `default` |
| `CONSUL_HTTP_ADDR` | Consul API address | `http://127.0.0.1:8500` |
| `CONSUL_HTTP_TOKEN` | Consul ACL token | (none) |

### Example with Environment Variables

```bash
export NOMAD_ADDR=https://nomad.example.com:4646
export NOMAD_TOKEN=your-nomad-token
export CONSUL_HTTP_ADDR=https://consul.example.com:8500
export CONSUL_HTTP_TOKEN=your-consul-token

xdsnap capture --service web
```

### Notes

- The tool queries Consul to discover services with Connect sidecar proxies, then maps them to Nomad allocations.
- Direct HTTP access to allocation IPs is attempted first; if unreachable, the tool falls back to `nomad alloc exec` with curl.
- When `--tcpdump` is enabled, the tool executes tcpdump inside the sidecar task. The resulting `.pcap` file is included in the snapshot archive.
- `--repeat` controls the number of capture cycles. `--duration` enforces a timeout for the entire session.
- The tool automatically detects sidecar tasks (e.g., `connect-proxy-*`, `envoy-sidecar`, `consul-dataplane`).

---

## Feature Requests

We welcome suggestions and ideas to improve xDSnap!

### How to Submit a Feature Request

If you have an idea for a new feature, please [open a new issue](https://github.com/markcampv/xdsnap/issues/new?template=feature_request.md) using the **Feature Request** template. Include:

- **Brief Description**: What feature would you like to see?
- **Use Case / Motivation**: How would you use it? What problem does it solve?
- **Proposed Changes**: Any anticipated CLI, output, or integration changes.
- **Alternatives Considered**: Current workarounds or existing tools you've tried.
- **Additional Context** *(Optional)*: Screenshots, logs, or examples.

---

Your input helps shape the direction of xDSnap. Thank you for contributing!
