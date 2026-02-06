# xDSnap Demo Environment

This folder contains a Vagrant setup to run Consul Connect + Nomad for testing xDSnap.

> **Note:** Consul Connect requires Linux (network namespaces). This demo uses Vagrant to run an Ubuntu VM.

## Prerequisites

- [Vagrant](https://www.vagrantup.com/downloads) - `brew install --cask vagrant`
- [QEMU](https://www.qemu.org/) - `brew install qemu`
- vagrant-qemu plugin - `vagrant plugin install vagrant-qemu`

## Quick Start

### 1. Start the VM

```bash
cd demo
vagrant up --provision
```

This provisions an Ubuntu VM with:
- Consul
- Nomad
- Docker
- CNI plugins

### 2. Start Consul and Nomad

```bash
vagrant ssh -c "/vagrant/start.sh"
```

Leave this terminal running. The UIs are available at:
- Consul: http://localhost:8500
- Nomad: http://localhost:4646

### 3. Deploy the sample job

In a new terminal:

```bash
cd demo
vagrant ssh -c "nomad job run /vagrant/echo.nomad.hcl"
```

This deploys two services connected via Consul service mesh:
- `echo-api` - Backend echo server
- `echo-client` - Frontend that calls the backend (http://localhost:9002)

### 4. Verify deployment

```bash
# Check job status
vagrant ssh -c "nomad job status echo"

# Check Consul services
vagrant ssh -c "consul catalog services"
```

You should see:
- `echo-api`
- `echo-api-sidecar-proxy`
- `echo-client`
- `echo-client-sidecar-proxy`

### 5. Capture Envoy snapshots with xDSnap

From your **host machine** (not inside the VM):

```bash
# Build xDSnap if needed
cd ..
go build -o xdsnap ./cmd/

# Capture all Connect allocations
./xdsnap capture

# Capture a specific service
./xdsnap capture --service echo-client

# Capture with trace logging
./xdsnap capture --service echo-api --enable-trace --duration 30
```

## Cleanup

Stop the services (Ctrl+C in the terminal running start.sh), then:

```bash
# Stop and remove the VM
vagrant destroy -f
```

Or to just stop without destroying:

```bash
vagrant halt
```

## Troubleshooting

### "No Connect allocations found"

Make sure the job is running and healthy:

```bash
vagrant ssh -c "nomad job status echo"
```

### Connection refused

Ensure Consul and Nomad are binding to 0.0.0.0 (the start.sh script handles this).

## References

- [Consul Service Mesh on Nomad](https://developer.hashicorp.com/nomad/docs/networking/consul/service-mesh)
- [Consul Connect Tutorial](https://developer.hashicorp.com/nomad/tutorials/integrate-consul/consul-service-mesh)
