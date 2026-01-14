#!/bin/bash
# Stop Consul and Nomad dev agents
# Run inside the Vagrant VM

echo "Stopping Nomad..."
sudo pkill -f "nomad agent" 2>/dev/null || true

echo "Stopping Consul..."
pkill -f "consul agent" 2>/dev/null || true

echo "Done"
