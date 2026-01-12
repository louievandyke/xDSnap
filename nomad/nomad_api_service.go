package nomad

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	nomadapi "github.com/hashicorp/nomad/api"
)

const EnvoyAdminPort = 19000

// AllocationInfo contains information about a Nomad allocation running Consul Connect
type AllocationInfo struct {
	ID          string
	Name        string
	JobID       string
	TaskGroup   string
	Namespace   string
	NodeID      string
	IP          string
	Tasks       []string
	SidecarTask string // detected envoy/connect-proxy task
}

// NomadApiService defines the interface for interacting with Nomad and Consul
type NomadApiService interface {
	// Execution
	ExecuteCommand(allocID, task string, command []string, stdout io.Writer) (int, error)
	ExecuteCommandWithStderr(allocID, task string, command []string, stdout, stderr io.Writer) (int, error)

	// Logs
	FetchTaskLogs(ctx context.Context, allocID, task string, logType string, follow bool, out io.Writer) error

	// Discovery
	ListTasks(allocID string) ([]string, error)
	GetAllocationIP(allocID string) (string, error)
	GetAllocation(allocID string) (*AllocationInfo, error)

	// Consul Integration
	FindConnectAllocations(namespace string) ([]AllocationInfo, error)
	FindConnectAllocationsByService(namespace, serviceName string) ([]AllocationInfo, error)

	// HTTP requests to Envoy (direct IP access)
	EnvoyAdminGET(allocIP string, port int, path string) ([]byte, error)
	EnvoyAdminPOST(allocIP string, port int, path string) error

	// Exec-based Envoy access (fallback when direct IP not reachable)
	EnvoyAdminGETViaExec(allocID, task string, port int, path string) ([]byte, error)
	EnvoyAdminPOSTViaExec(allocID, task string, port int, path string) error
}

// NomadApiServiceImpl implements NomadApiService
type NomadApiServiceImpl struct {
	nomadClient  *nomadapi.Client
	consulClient *consulapi.Client
	namespace    string
}

var _ NomadApiService = &NomadApiServiceImpl{}

// NewNomadApiService creates a new NomadApiService
func NewNomadApiService(nomadClient *nomadapi.Client, consulClient *consulapi.Client, namespace string) NomadApiService {
	return &NomadApiServiceImpl{
		nomadClient:  nomadClient,
		consulClient: consulClient,
		namespace:    namespace,
	}
}

// NewNomadApiServiceFromEnv creates a NomadApiService using environment variables
func NewNomadApiServiceFromEnv(namespace string) (NomadApiService, error) {
	// Create Nomad client
	nomadConfig := nomadapi.DefaultConfig()
	if addr := os.Getenv("NOMAD_ADDR"); addr != "" {
		nomadConfig.Address = addr
	}
	if token := os.Getenv("NOMAD_TOKEN"); token != "" {
		nomadConfig.SecretID = token
	}
	if namespace != "" {
		nomadConfig.Namespace = namespace
	}

	nomadClient, err := nomadapi.NewClient(nomadConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Nomad client: %w", err)
	}

	// Create Consul client
	consulConfig := consulapi.DefaultConfig()
	if addr := os.Getenv("CONSUL_HTTP_ADDR"); addr != "" {
		consulConfig.Address = addr
	}
	if token := os.Getenv("CONSUL_HTTP_TOKEN"); token != "" {
		consulConfig.Token = token
	}

	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %w", err)
	}

	return &NomadApiServiceImpl{
		nomadClient:  nomadClient,
		consulClient: consulClient,
		namespace:    namespace,
	}, nil
}

// ExecuteCommand executes a command in a task and returns the exit code
func (n *NomadApiServiceImpl) ExecuteCommand(allocID, task string, command []string, stdout io.Writer) (int, error) {
	return n.ExecuteCommandWithStderr(allocID, task, command, stdout, io.Discard)
}

// ExecuteCommandWithStderr executes a command in a task with separate stdout/stderr
func (n *NomadApiServiceImpl) ExecuteCommandWithStderr(allocID, task string, command []string, stdout, stderr io.Writer) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Set up signal handling for resize (not used but required by API)
	sizeCh := make(chan nomadapi.TerminalSize)

	alloc, _, err := n.nomadClient.Allocations().Info(allocID, nil)
	if err != nil {
		return -1, fmt.Errorf("failed to get allocation info: %w", err)
	}

	exitCode, err := n.nomadClient.Allocations().Exec(
		ctx,
		alloc,
		task,
		false, // tty
		command,
		nil,    // stdin
		stdout,
		stderr,
		sizeCh,
		nil, // query options
	)
	if err != nil {
		return -1, fmt.Errorf("exec failed: %w", err)
	}

	return exitCode, nil
}

// FetchTaskLogs fetches logs from a task
func (n *NomadApiServiceImpl) FetchTaskLogs(ctx context.Context, allocID, task string, logType string, follow bool, out io.Writer) error {
	alloc, _, err := n.nomadClient.Allocations().Info(allocID, nil)
	if err != nil {
		return fmt.Errorf("failed to get allocation info: %w", err)
	}

	// Create a cancel channel from context
	cancel := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(cancel)
	}()

	// Also handle OS signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			close(cancel)
		case <-ctx.Done():
		}
	}()

	frames, errCh := n.nomadClient.AllocFS().Logs(
		alloc,
		follow,
		task,
		logType, // "stdout" or "stderr"
		"start", // origin
		0,       // offset
		cancel,
		nil, // query options
	)

	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return nil
			}
			if frame != nil {
				out.Write(frame.Data)
			}
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("log streaming error: %w", err)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ListTasks returns the list of tasks in an allocation
func (n *NomadApiServiceImpl) ListTasks(allocID string) ([]string, error) {
	alloc, _, err := n.nomadClient.Allocations().Info(allocID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get allocation info: %w", err)
	}

	var tasks []string
	for taskName := range alloc.TaskStates {
		tasks = append(tasks, taskName)
	}
	return tasks, nil
}

// GetAllocationIP returns the IP address of an allocation
func (n *NomadApiServiceImpl) GetAllocationIP(allocID string) (string, error) {
	alloc, _, err := n.nomadClient.Allocations().Info(allocID, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get allocation info: %w", err)
	}

	// Try to get the allocation's network IP
	if alloc.AllocatedResources != nil && alloc.AllocatedResources.Shared.Networks != nil {
		for _, network := range alloc.AllocatedResources.Shared.Networks {
			if network.IP != "" {
				return network.IP, nil
			}
		}
	}

	// Fallback: try to get from task resources
	for _, resources := range alloc.AllocatedResources.Tasks {
		for _, network := range resources.Networks {
			if network.IP != "" {
				return network.IP, nil
			}
		}
	}

	return "", fmt.Errorf("no IP address found for allocation %s", allocID)
}

// GetAllocation returns detailed information about an allocation
func (n *NomadApiServiceImpl) GetAllocation(allocID string) (*AllocationInfo, error) {
	alloc, _, err := n.nomadClient.Allocations().Info(allocID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get allocation info: %w", err)
	}

	info := &AllocationInfo{
		ID:        alloc.ID,
		Name:      alloc.Name,
		JobID:     alloc.JobID,
		TaskGroup: alloc.TaskGroup,
		Namespace: alloc.Namespace,
		NodeID:    alloc.NodeID,
	}

	// Get tasks
	for taskName := range alloc.TaskStates {
		info.Tasks = append(info.Tasks, taskName)
	}

	// Detect sidecar task
	info.SidecarTask = detectSidecarTask(info.Tasks)

	// Get IP
	info.IP, _ = n.GetAllocationIP(allocID)

	return info, nil
}

// FindConnectAllocations finds all allocations running Consul Connect sidecars
func (n *NomadApiServiceImpl) FindConnectAllocations(namespace string) ([]AllocationInfo, error) {
	return n.FindConnectAllocationsByService(namespace, "")
}

// FindConnectAllocationsByService finds allocations for a specific Consul Connect service
func (n *NomadApiServiceImpl) FindConnectAllocationsByService(namespace, serviceName string) ([]AllocationInfo, error) {
	var results []AllocationInfo

	// Query Consul for services with sidecar proxies
	services, _, err := n.consulClient.Catalog().Services(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query Consul services: %w", err)
	}

	// Find sidecar proxy services
	var proxyServices []string
	for svc := range services {
		if strings.HasSuffix(svc, "-sidecar-proxy") {
			// If filtering by service name, check if this matches
			if serviceName != "" {
				baseName := strings.TrimSuffix(svc, "-sidecar-proxy")
				if baseName != serviceName {
					continue
				}
			}
			proxyServices = append(proxyServices, svc)
		}
	}

	// For each proxy service, get the instances and map to Nomad allocations
	for _, proxySvc := range proxyServices {
		instances, _, err := n.consulClient.Health().Service(proxySvc, "", true, nil)
		if err != nil {
			continue
		}

		for _, instance := range instances {
			// Extract Nomad allocation ID from service metadata or ID
			// Consul Connect services registered by Nomad have allocation info in metadata
			allocID := extractAllocIDFromService(instance.Service)
			if allocID == "" {
				continue
			}

			// Get full allocation info from Nomad
			allocInfo, err := n.GetAllocation(allocID)
			if err != nil {
				continue
			}

			// Filter by namespace if specified
			if namespace != "" && allocInfo.Namespace != namespace {
				continue
			}

			results = append(results, *allocInfo)
		}
	}

	// Fallback: If no results from Consul, scan Nomad allocations directly
	if len(results) == 0 {
		results, err = n.scanNomadForConnectAllocations(namespace)
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// scanNomadForConnectAllocations scans Nomad directly for Connect allocations
func (n *NomadApiServiceImpl) scanNomadForConnectAllocations(namespace string) ([]AllocationInfo, error) {
	var results []AllocationInfo

	queryOpts := &nomadapi.QueryOptions{}
	if namespace != "" {
		queryOpts.Namespace = namespace
	} else {
		queryOpts.Namespace = "*" // All namespaces
	}

	allocs, _, err := n.nomadClient.Allocations().List(queryOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list allocations: %w", err)
	}

	for _, allocStub := range allocs {
		// Skip non-running allocations
		if allocStub.ClientStatus != "running" {
			continue
		}

		// Get full allocation info
		alloc, _, err := n.nomadClient.Allocations().Info(allocStub.ID, nil)
		if err != nil {
			continue
		}

		// Check if this allocation has Connect enabled
		if !hasConnectSidecar(alloc) {
			continue
		}

		allocInfo, err := n.GetAllocation(allocStub.ID)
		if err != nil {
			continue
		}

		results = append(results, *allocInfo)
	}

	return results, nil
}

// EnvoyAdminGET makes a GET request to the Envoy admin API via direct IP
func (n *NomadApiServiceImpl) EnvoyAdminGET(allocIP string, port int, path string) ([]byte, error) {
	url := fmt.Sprintf("http://%s:%d%s", allocIP, port, path)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return body, nil
}

// EnvoyAdminPOST makes a POST request to the Envoy admin API via direct IP
func (n *NomadApiServiceImpl) EnvoyAdminPOST(allocIP string, port int, path string) error {
	url := fmt.Sprintf("http://%s:%d%s", allocIP, port, path)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("POST %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	return nil
}

// EnvoyAdminGETViaExec makes a GET request to Envoy admin via exec (fallback)
func (n *NomadApiServiceImpl) EnvoyAdminGETViaExec(allocID, task string, port int, path string) ([]byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := []string{"sh", "-c", fmt.Sprintf("curl -s http://127.0.0.1:%d%s", port, path)}
	_, err := n.ExecuteCommandWithStderr(allocID, task, cmd, &stdout, &stderr)
	if err != nil {
		return nil, fmt.Errorf("exec curl failed: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// EnvoyAdminPOSTViaExec makes a POST request to Envoy admin via exec (fallback)
func (n *NomadApiServiceImpl) EnvoyAdminPOSTViaExec(allocID, task string, port int, path string) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := []string{"sh", "-c", fmt.Sprintf("curl -s -X POST http://127.0.0.1:%d%s", port, path)}
	_, err := n.ExecuteCommandWithStderr(allocID, task, cmd, &stdout, &stderr)
	if err != nil {
		return fmt.Errorf("exec curl failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// Helper functions

// detectSidecarTask identifies the Envoy/Connect sidecar task from a list of tasks
func detectSidecarTask(tasks []string) string {
	// Common sidecar task name patterns
	candidates := []string{
		"connect-proxy-",
		"envoy-sidecar",
		"consul-dataplane",
		"mesh-gateway",
		"api-gateway",
		"terminating-gateway",
		"ingress-gateway",
	}

	for _, task := range tasks {
		for _, candidate := range candidates {
			if strings.HasPrefix(task, candidate) || task == candidate {
				return task
			}
		}
	}

	// Fallback: look for any task with "proxy" or "envoy" in the name
	for _, task := range tasks {
		lower := strings.ToLower(task)
		if strings.Contains(lower, "proxy") || strings.Contains(lower, "envoy") {
			return task
		}
	}

	return ""
}

// extractAllocIDFromService extracts the Nomad allocation ID from a Consul service
func extractAllocIDFromService(svc *consulapi.AgentService) string {
	// Check service metadata for allocation ID
	if svc.Meta != nil {
		if allocID, ok := svc.Meta["alloc_id"]; ok {
			return allocID
		}
	}

	// Try to extract from service ID (format: _nomad-task-<alloc_id>-...)
	if strings.HasPrefix(svc.ID, "_nomad-task-") {
		parts := strings.Split(svc.ID, "-")
		if len(parts) >= 3 {
			// The allocation ID is a UUID, which is 36 characters (8-4-4-4-12)
			// Find the UUID in the parts
			for i := 2; i < len(parts); i++ {
				// Try to find a sequence that forms a UUID
				if len(parts[i]) == 8 && i+4 < len(parts) {
					potentialUUID := strings.Join(parts[i:i+5], "-")
					if len(potentialUUID) == 36 {
						return potentialUUID
					}
				}
			}
		}
	}

	return ""
}

// hasConnectSidecar checks if an allocation has Consul Connect enabled
func hasConnectSidecar(alloc *nomadapi.Allocation) bool {
	if alloc.Job == nil {
		return false
	}

	for _, tg := range alloc.Job.TaskGroups {
		if tg.Name == nil || *tg.Name != alloc.TaskGroup {
			continue
		}

		// Check for Connect stanza
		if tg.Consul != nil {
			return true
		}

		// Check services for Connect configuration
		for _, svc := range tg.Services {
			if svc.Connect != nil {
				return true
			}
		}

		// Check for sidecar tasks
		for _, task := range tg.Tasks {
			if strings.HasPrefix(task.Name, "connect-proxy-") {
				return true
			}
		}
	}

	return false
}
