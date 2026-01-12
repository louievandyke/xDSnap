package consul

import (
	"fmt"
	"os"
	"strings"

	consulapi "github.com/hashicorp/consul/api"
)

// ServiceInstance represents a Consul Connect service instance
type ServiceInstance struct {
	ServiceName   string
	ServiceID     string
	Address       string
	Port          int
	ProxyService  string
	ProxyAddress  string
	ProxyPort     int
	AllocID       string
	Node          string
	Namespace     string
	Datacenter    string
	Tags          []string
	Meta          map[string]string
	HealthStatus  string
}

// Discovery provides methods for discovering Consul Connect services
type Discovery struct {
	client *consulapi.Client
}

// NewDiscovery creates a new Discovery instance
func NewDiscovery(client *consulapi.Client) *Discovery {
	return &Discovery{client: client}
}

// NewDiscoveryFromEnv creates a Discovery instance using environment variables
func NewDiscoveryFromEnv() (*Discovery, error) {
	config := consulapi.DefaultConfig()
	if addr := os.Getenv("CONSUL_HTTP_ADDR"); addr != "" {
		config.Address = addr
	}
	if token := os.Getenv("CONSUL_HTTP_TOKEN"); token != "" {
		config.Token = token
	}

	client, err := consulapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %w", err)
	}

	return &Discovery{client: client}, nil
}

// ListConnectServices returns all services that have Consul Connect sidecars
func (d *Discovery) ListConnectServices() ([]string, error) {
	services, _, err := d.client.Catalog().Services(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var connectServices []string
	seen := make(map[string]bool)

	for svc := range services {
		// Look for sidecar proxy services
		if strings.HasSuffix(svc, "-sidecar-proxy") {
			baseName := strings.TrimSuffix(svc, "-sidecar-proxy")
			if !seen[baseName] {
				connectServices = append(connectServices, baseName)
				seen[baseName] = true
			}
		}
	}

	return connectServices, nil
}

// GetServiceInstances returns all instances of a Consul Connect service
func (d *Discovery) GetServiceInstances(serviceName string, healthyOnly bool) ([]ServiceInstance, error) {
	var results []ServiceInstance

	// Get the main service instances
	healthStatus := ""
	if healthyOnly {
		healthStatus = "passing"
	}

	entries, _, err := d.client.Health().Service(serviceName, "", healthyOnly, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get service %s: %w", serviceName, err)
	}

	// Also get the sidecar proxy instances
	proxyServiceName := serviceName + "-sidecar-proxy"
	proxyEntries, _, err := d.client.Health().Service(proxyServiceName, "", healthyOnly, nil)
	if err != nil {
		// Not all services have explicit proxy entries, continue
		proxyEntries = nil
	}

	// Build a map of proxy info by node
	proxyByNode := make(map[string]*consulapi.ServiceEntry)
	for _, entry := range proxyEntries {
		proxyByNode[entry.Node.Node] = entry
	}

	for _, entry := range entries {
		instance := ServiceInstance{
			ServiceName:  entry.Service.Service,
			ServiceID:    entry.Service.ID,
			Address:      entry.Service.Address,
			Port:         entry.Service.Port,
			Node:         entry.Node.Node,
			Datacenter:   entry.Node.Datacenter,
			Tags:         entry.Service.Tags,
			Meta:         entry.Service.Meta,
			HealthStatus: healthStatus,
		}

		// Set address fallback to node address
		if instance.Address == "" {
			instance.Address = entry.Node.Address
		}

		// Try to extract allocation ID from service metadata
		if entry.Service.Meta != nil {
			if allocID, ok := entry.Service.Meta["alloc_id"]; ok {
				instance.AllocID = allocID
			}
			if ns, ok := entry.Service.Meta["namespace"]; ok {
				instance.Namespace = ns
			}
		}

		// Try to extract from service ID if not in metadata
		if instance.AllocID == "" {
			instance.AllocID = extractAllocIDFromServiceID(entry.Service.ID)
		}

		// Add proxy information if available
		if proxy, ok := proxyByNode[entry.Node.Node]; ok {
			instance.ProxyService = proxy.Service.Service
			instance.ProxyAddress = proxy.Service.Address
			if instance.ProxyAddress == "" {
				instance.ProxyAddress = proxy.Node.Address
			}
			instance.ProxyPort = proxy.Service.Port
		}

		results = append(results, instance)
	}

	return results, nil
}

// GetConnectProxyInstances returns all sidecar proxy instances for a service
func (d *Discovery) GetConnectProxyInstances(serviceName string, healthyOnly bool) ([]ServiceInstance, error) {
	proxyServiceName := serviceName + "-sidecar-proxy"
	return d.GetServiceInstances(proxyServiceName, healthyOnly)
}

// GetAllConnectProxyInstances returns all sidecar proxy instances in the catalog
func (d *Discovery) GetAllConnectProxyInstances(healthyOnly bool) ([]ServiceInstance, error) {
	services, err := d.ListConnectServices()
	if err != nil {
		return nil, err
	}

	var allInstances []ServiceInstance
	for _, svc := range services {
		instances, err := d.GetServiceInstances(svc, healthyOnly)
		if err != nil {
			continue // Skip services we can't query
		}
		allInstances = append(allInstances, instances...)
	}

	return allInstances, nil
}

// extractAllocIDFromServiceID attempts to extract a Nomad allocation ID from a Consul service ID
func extractAllocIDFromServiceID(serviceID string) string {
	// Nomad registers services with IDs like: _nomad-task-<alloc_id>-<group>-<task>-<service>
	if !strings.HasPrefix(serviceID, "_nomad-task-") {
		return ""
	}

	// Remove the prefix
	remainder := strings.TrimPrefix(serviceID, "_nomad-task-")

	// The allocation ID is a UUID (36 chars: 8-4-4-4-12)
	// Find it in the remainder
	parts := strings.Split(remainder, "-")
	if len(parts) < 5 {
		return ""
	}

	// Try to reconstruct the UUID from the first 5 parts
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	for i := 0; i <= len(parts)-5; i++ {
		if len(parts[i]) == 8 && len(parts[i+1]) == 4 && len(parts[i+2]) == 4 && len(parts[i+3]) == 4 && len(parts[i+4]) == 12 {
			return strings.Join(parts[i:i+5], "-")
		}
	}

	return ""
}

// GetEnvoyAdminPort returns the Envoy admin port for a service instance
// This is typically exposed on a dynamic port or the default 19000
func (d *Discovery) GetEnvoyAdminPort(instance ServiceInstance) int {
	// Check if there's an admin port in metadata
	if instance.Meta != nil {
		if port, ok := instance.Meta["envoy_admin_port"]; ok {
			var p int
			fmt.Sscanf(port, "%d", &p)
			if p > 0 {
				return p
			}
		}
	}

	// Default Envoy admin port
	return 19000
}
