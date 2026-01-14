#!/bin/bash
# Start Consul and Nomad in dev mode for testing Consul Connect
# Run this inside the Vagrant VM: vagrant ssh -c "/vagrant/start.sh"

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== xDSnap Demo Environment ===${NC}"
echo ""

# Check prerequisites
echo "Checking prerequisites..."

if ! command -v consul &> /dev/null; then
    echo -e "${RED}Error: consul not found in PATH${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓${NC} Consul: $(consul version | head -1)"

if ! command -v nomad &> /dev/null; then
    echo -e "${RED}Error: nomad not found in PATH${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓${NC} Nomad: $(nomad version | head -1)"

if ! command -v docker &> /dev/null; then
    echo -e "${RED}Error: docker not found${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓${NC} Docker: $(docker --version)"

# Check for CNI plugins
CNI_PATH="/opt/cni/bin"
if [ ! -d "$CNI_PATH" ]; then
    echo -e "${RED}Error: CNI plugins not found at $CNI_PATH${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓${NC} CNI plugins installed"

echo ""
echo -e "${GREEN}Starting Consul in dev mode...${NC}"
consul agent -dev -client=0.0.0.0 &
CONSUL_PID=$!
echo "Consul PID: $CONSUL_PID"

# Wait for Consul to be ready
echo "Waiting for Consul to be ready..."
sleep 3

echo ""
echo -e "${GREEN}Starting Nomad in dev-connect mode...${NC}"
sudo nomad agent -dev-connect -bind=0.0.0.0 &
NOMAD_PID=$!
echo "Nomad PID: $NOMAD_PID"

# Wait for Nomad to be ready
echo "Waiting for Nomad to be ready..."
sleep 5

echo ""
echo -e "${GREEN}=== Environment Ready ===${NC}"
echo ""
echo "Consul UI: http://localhost:8500"
echo "Nomad UI:  http://localhost:4646"
echo ""
echo "Deploy the sample jobs (from inside VM):"
echo "  nomad job run /vagrant/countdash.nomad.hcl"
echo ""
echo "Then capture snapshots (from host):"
echo "  ./xdsnap capture --service count-dashboard"
echo ""
echo -e "${YELLOW}Press Ctrl+C to stop all services${NC}"

# Trap Ctrl+C to cleanup
cleanup() {
    echo ""
    echo -e "${YELLOW}Stopping services...${NC}"
    sudo kill $NOMAD_PID 2>/dev/null || true
    kill $CONSUL_PID 2>/dev/null || true
    echo -e "${GREEN}Done${NC}"
    exit 0
}

trap cleanup SIGINT SIGTERM

# Wait for processes
wait
