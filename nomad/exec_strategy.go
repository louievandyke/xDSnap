package nomad

import (
	"bytes"
	"fmt"
	"log"
	"strings"
)

// HTTPMethod represents a method for making HTTP requests from inside a container.
type HTTPMethod int

const (
	MethodCurl    HTTPMethod = iota // curl -s
	MethodWget                      // wget -qO-
	MethodPython3                   // python3 urllib
	MethodNode                      // node http
	MethodBashTCP                   // bash /dev/tcp
)

func (m HTTPMethod) String() string {
	switch m {
	case MethodCurl:
		return "curl"
	case MethodWget:
		return "wget"
	case MethodPython3:
		return "python3"
	case MethodNode:
		return "node"
	case MethodBashTCP:
		return "bash"
	default:
		return "unknown"
	}
}

// ExecStrategy describes which task and HTTP method to use for Envoy admin access.
type ExecStrategy struct {
	Task   string
	Method HTTPMethod
}

// probeCommands are lightweight commands used to detect available HTTP tools.
var probeCommands = []struct {
	Method  HTTPMethod
	Command []string
}{
	{MethodCurl, []string{"curl", "--version"}},
	{MethodWget, []string{"wget", "--version"}},
	{MethodPython3, []string{"python3", "--version"}},
	{MethodNode, []string{"node", "--version"}},
	{MethodBashTCP, []string{"bash", "-c", "echo ok"}},
}

// ProbeHTTPCapability probes a single task for available HTTP methods.
// Returns the best available method and true, or false if none found.
func ProbeHTTPCapability(svc NomadApiService, allocID, task string) (HTTPMethod, bool) {
	for _, probe := range probeCommands {
		var stdout, stderr bytes.Buffer
		exitCode, err := svc.ExecuteCommandWithStderr(allocID, task, probe.Command, &stdout, &stderr)
		if err == nil && exitCode == 0 {
			return probe.Method, true
		}
	}
	return 0, false
}

// ResolveExecStrategy iterates through tasks in order, probes each for HTTP
// capabilities, and returns the first working (task, method) pair.
// taskOrder should be [sidecarTask, ...otherTasks].
func ResolveExecStrategy(svc NomadApiService, allocID string, taskOrder []string) (*ExecStrategy, error) {
	var tried []string
	for _, task := range taskOrder {
		log.Printf("Probing task %q for HTTP capabilities...", task)
		method, ok := ProbeHTTPCapability(svc, allocID, task)
		if ok {
			if task == taskOrder[0] {
				log.Printf("Using %s in task %q for Envoy admin access", method, task)
			} else {
				log.Printf("Using %s in sibling task %q for Envoy admin access (shared network namespace)", method, task)
			}
			return &ExecStrategy{Task: task, Method: method}, nil
		}
		log.Printf("  no tools found in task %q", task)
		tried = append(tried, task)
	}

	return nil, fmt.Errorf(
		"no HTTP tool found in any task for allocation %s\n  Tried: %s\n  Hint: ensure curl, wget, python3, node, or bash is available in at least one task",
		allocID[:8],
		strings.Join(tried, ", "),
	)
}

// BuildGETCommand builds the exec command for a GET request using the given method.
func BuildGETCommand(method HTTPMethod, port int, path string) []string {
	switch method {
	case MethodCurl:
		return []string{"curl", "-s", fmt.Sprintf("http://127.0.0.2:%d%s", port, path)}
	case MethodWget:
		return []string{"wget", "-qO-", fmt.Sprintf("http://127.0.0.2:%d%s", port, path)}
	case MethodPython3:
		return []string{"python3", "-c",
			fmt.Sprintf(`import urllib.request,sys;sys.stdout.buffer.write(urllib.request.urlopen("http://127.0.0.2:%d%s").read())`, port, path)}
	case MethodNode:
		return []string{"node", "-e",
			fmt.Sprintf(`var http=require("http");http.get("http://127.0.0.2:%d%s",function(r){var d=[];r.on("data",function(c){d.push(c)});r.on("end",function(){process.stdout.write(Buffer.concat(d))})}).on("error",function(){process.exit(1)})`, port, path)}
	case MethodBashTCP:
		bashCmd := fmt.Sprintf(
			`exec 3<>/dev/tcp/127.0.0.2/%d; echo -e "GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&3; cat <&3`,
			port, path,
		)
		return []string{"bash", "-c", bashCmd}
	default:
		return nil
	}
}

// BuildPOSTCommand builds the exec command for a POST request using the given method.
func BuildPOSTCommand(method HTTPMethod, port int, path string) []string {
	switch method {
	case MethodCurl:
		return []string{"curl", "-s", "-X", "POST", fmt.Sprintf("http://127.0.0.2:%d%s", port, path)}
	case MethodWget:
		return []string{"wget", "-qO-", "--post-data=", fmt.Sprintf("http://127.0.0.2:%d%s", port, path)}
	case MethodPython3:
		return []string{"python3", "-c",
			fmt.Sprintf(`import urllib.request;urllib.request.urlopen(urllib.request.Request("http://127.0.0.2:%d%s",data=b"",method="POST"))`, port, path)}
	case MethodNode:
		return []string{"node", "-e",
			fmt.Sprintf(`var http=require("http");var r=http.request({hostname:"127.0.0.2",port:%d,path:"%s",method:"POST"},function(res){res.resume()});r.on("error",function(){process.exit(1)});r.end()`, port, path)}
	case MethodBashTCP:
		bashCmd := fmt.Sprintf(
			`exec 3<>/dev/tcp/127.0.0.2/%d; echo -e "POST %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 0\r\n\r\n" >&3; cat <&3`,
			port, path,
		)
		return []string{"bash", "-c", bashCmd}
	default:
		return nil
	}
}
