package nomad

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

// mockExecResponse defines what a mocked exec call returns.
type mockExecResponse struct {
	exitCode int
	stdout   string
	err      error
}

// mockNomadService implements NomadApiService for testing.
// execResponses maps "task:cmd[0]" to a response.
type mockNomadService struct {
	execResponses map[string]mockExecResponse
}

func (m *mockNomadService) key(task string, command []string) string {
	cmd0 := ""
	if len(command) > 0 {
		cmd0 = command[0]
	}
	return task + ":" + cmd0
}

func (m *mockNomadService) ExecuteCommand(allocID, task string, command []string, stdout io.Writer) (int, error) {
	return m.ExecuteCommandWithStderr(allocID, task, command, stdout, io.Discard)
}

func (m *mockNomadService) ExecuteCommandWithStderr(allocID, task string, command []string, stdout, stderr io.Writer) (int, error) {
	k := m.key(task, command)
	if resp, ok := m.execResponses[k]; ok {
		if resp.stdout != "" {
			stdout.Write([]byte(resp.stdout))
		}
		return resp.exitCode, resp.err
	}
	// Simulate command not found
	stderr.Write([]byte(fmt.Sprintf("%s: not found\n", command[0])))
	return 127, fmt.Errorf("exec failed: command not found")
}

func (m *mockNomadService) FetchTaskLogs(ctx context.Context, allocID, task string, logType string, follow bool, out io.Writer) error {
	return nil
}

func (m *mockNomadService) ListTasks(allocID string) ([]string, error) {
	return nil, nil
}

func (m *mockNomadService) GetAllocation(allocID string) (*AllocationInfo, error) {
	return nil, nil
}

func (m *mockNomadService) FindConnectAllocations(namespace string) ([]AllocationInfo, error) {
	return nil, nil
}

func (m *mockNomadService) FindConnectAllocationsByService(namespace, serviceName string) ([]AllocationInfo, error) {
	return nil, nil
}

func (m *mockNomadService) EnvoyAdminGETViaExec(allocID, task string, port int, path string) ([]byte, error) {
	return nil, nil
}

func (m *mockNomadService) EnvoyAdminPOSTViaExec(allocID, task string, port int, path string) error {
	return nil
}

func (m *mockNomadService) EnvoyAdminGET(allocID string, strategy *ExecStrategy, port int, path string) ([]byte, error) {
	cmd := BuildGETCommand(strategy.Method, port, path)
	var stdout, stderr bytes.Buffer
	_, err := m.ExecuteCommandWithStderr(allocID, strategy.Task, cmd, &stdout, &stderr)
	if err != nil {
		return nil, err
	}
	body := stdout.Bytes()
	if strategy.Method == MethodBashTCP {
		if idx := bytes.Index(body, []byte("\r\n\r\n")); idx != -1 {
			body = body[idx+4:]
		}
		body = decodeChunked(body)
	}
	return body, nil
}

func (m *mockNomadService) EnvoyAdminPOST(allocID string, strategy *ExecStrategy, port int, path string) error {
	cmd := BuildPOSTCommand(strategy.Method, port, path)
	var stdout, stderr bytes.Buffer
	_, err := m.ExecuteCommandWithStderr(allocID, strategy.Task, cmd, &stdout, &stderr)
	return err
}

// --- Tests ---

func TestHTTPMethodString(t *testing.T) {
	tests := []struct {
		method HTTPMethod
		want   string
	}{
		{MethodCurl, "curl"},
		{MethodWget, "wget"},
		{MethodPython3, "python3"},
		{MethodNode, "node"},
		{MethodBashTCP, "bash"},
		{HTTPMethod(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.method.String(); got != tt.want {
			t.Errorf("HTTPMethod(%d).String() = %q, want %q", tt.method, got, tt.want)
		}
	}
}

func TestBuildGETCommand(t *testing.T) {
	tests := []struct {
		name   string
		method HTTPMethod
		port   int
		path   string
		want   []string
	}{
		{
			name:   "curl",
			method: MethodCurl,
			port:   19001,
			path:   "/stats",
			want:   []string{"curl", "-s", "http://127.0.0.2:19001/stats"},
		},
		{
			name:   "wget",
			method: MethodWget,
			port:   19001,
			path:   "/config_dump",
			want:   []string{"wget", "-qO-", "http://127.0.0.2:19001/config_dump"},
		},
		{
			name:   "python3",
			method: MethodPython3,
			port:   19001,
			path:   "/stats",
			want: []string{"python3", "-c",
				`import urllib.request,sys;sys.stdout.buffer.write(urllib.request.urlopen("http://127.0.0.2:19001/stats").read())`,
			},
		},
		{
			name:   "node",
			method: MethodNode,
			port:   19001,
			path:   "/config_dump",
			want: []string{"node", "-e",
				`var http=require("http");http.get("http://127.0.0.2:19001/config_dump",function(r){var d=[];r.on("data",function(c){d.push(c)});r.on("end",function(){process.stdout.write(Buffer.concat(d))})}).on("error",function(){process.exit(1)})`,
			},
		},
		{
			name:   "bash",
			method: MethodBashTCP,
			port:   19001,
			path:   "/clusters",
			want: []string{"bash", "-c",
				`exec 3<>/dev/tcp/127.0.0.2/19001; echo -e "GET /clusters HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&3; cat <&3`,
			},
		},
		{
			name:   "unknown returns nil",
			method: HTTPMethod(99),
			port:   19001,
			path:   "/stats",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildGETCommand(tt.method, tt.port, tt.path)
			if tt.want == nil {
				if got != nil {
					t.Errorf("BuildGETCommand() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("BuildGETCommand() len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("BuildGETCommand()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildPOSTCommand(t *testing.T) {
	tests := []struct {
		name   string
		method HTTPMethod
		port   int
		path   string
		want   []string
	}{
		{
			name:   "curl",
			method: MethodCurl,
			port:   19001,
			path:   "/logging?level=debug",
			want:   []string{"curl", "-s", "-X", "POST", "http://127.0.0.2:19001/logging?level=debug"},
		},
		{
			name:   "wget",
			method: MethodWget,
			port:   19001,
			path:   "/logging?level=debug",
			want:   []string{"wget", "-qO-", "--post-data=", "http://127.0.0.2:19001/logging?level=debug"},
		},
		{
			name:   "python3",
			method: MethodPython3,
			port:   19001,
			path:   "/logging?level=debug",
			want: []string{"python3", "-c",
				`import urllib.request;urllib.request.urlopen(urllib.request.Request("http://127.0.0.2:19001/logging?level=debug",data=b"",method="POST"))`,
			},
		},
		{
			name:   "node",
			method: MethodNode,
			port:   19001,
			path:   "/logging?level=debug",
			want: []string{"node", "-e",
				`var http=require("http");var r=http.request({hostname:"127.0.0.2",port:19001,path:"/logging?level=debug",method:"POST"},function(res){res.resume()});r.on("error",function(){process.exit(1)});r.end()`,
			},
		},
		{
			name:   "bash",
			method: MethodBashTCP,
			port:   19001,
			path:   "/logging?level=debug",
			want: []string{"bash", "-c",
				`exec 3<>/dev/tcp/127.0.0.2/19001; echo -e "POST /logging?level=debug HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 0\r\n\r\n" >&3; cat <&3`,
			},
		},
		{
			name:   "unknown returns nil",
			method: HTTPMethod(99),
			port:   19001,
			path:   "/stats",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPOSTCommand(tt.method, tt.port, tt.path)
			if tt.want == nil {
				if got != nil {
					t.Errorf("BuildPOSTCommand() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("BuildPOSTCommand() len = %d, want %d\ngot:  %v\nwant: %v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("BuildPOSTCommand()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestProbeHTTPCapability(t *testing.T) {
	allocID := "abcdef12-3456-7890-abcd-ef1234567890"

	tests := []struct {
		name       string
		task       string
		responses  map[string]mockExecResponse
		wantMethod HTTPMethod
		wantOK     bool
	}{
		{
			name: "curl available",
			task: "web",
			responses: map[string]mockExecResponse{
				"web:curl": {exitCode: 0, stdout: "curl 7.68.0"},
			},
			wantMethod: MethodCurl,
			wantOK:     true,
		},
		{
			name: "only wget available",
			task: "web",
			responses: map[string]mockExecResponse{
				"web:wget": {exitCode: 0, stdout: "GNU Wget 1.20"},
			},
			wantMethod: MethodWget,
			wantOK:     true,
		},
		{
			name: "only bash available",
			task: "proxy",
			responses: map[string]mockExecResponse{
				"proxy:bash": {exitCode: 0, stdout: "ok\n"},
			},
			wantMethod: MethodBashTCP,
			wantOK:     true,
		},
		{
			name: "only python3 available",
			task: "app",
			responses: map[string]mockExecResponse{
				"app:python3": {exitCode: 0, stdout: "Python 3.11.0"},
			},
			wantMethod: MethodPython3,
			wantOK:     true,
		},
		{
			name: "only node available",
			task: "app",
			responses: map[string]mockExecResponse{
				"app:node": {exitCode: 0, stdout: "v18.0.0"},
			},
			wantMethod: MethodNode,
			wantOK:     true,
		},
		{
			name: "python3 preferred over node when no curl/wget",
			task: "app",
			responses: map[string]mockExecResponse{
				"app:python3": {exitCode: 0, stdout: "Python 3.11.0"},
				"app:node":    {exitCode: 0, stdout: "v18.0.0"},
				"app:bash":    {exitCode: 0, stdout: "ok\n"},
			},
			wantMethod: MethodPython3,
			wantOK:     true,
		},
		{
			name:       "nothing available (distroless)",
			task:       "envoy",
			responses:  map[string]mockExecResponse{},
			wantMethod: 0,
			wantOK:     false,
		},
		{
			name: "curl preferred over wget and bash",
			task: "web",
			responses: map[string]mockExecResponse{
				"web:curl": {exitCode: 0, stdout: "curl 7.68.0"},
				"web:wget": {exitCode: 0, stdout: "GNU Wget 1.20"},
				"web:bash": {exitCode: 0, stdout: "ok\n"},
			},
			wantMethod: MethodCurl,
			wantOK:     true,
		},
		{
			name: "wget preferred over bash when no curl",
			task: "web",
			responses: map[string]mockExecResponse{
				"web:wget": {exitCode: 0, stdout: "GNU Wget 1.20"},
				"web:bash": {exitCode: 0, stdout: "ok\n"},
			},
			wantMethod: MethodWget,
			wantOK:     true,
		},
		{
			name: "non-zero exit code treated as unavailable",
			task: "web",
			responses: map[string]mockExecResponse{
				"web:curl": {exitCode: 1, stdout: ""},
				"web:wget": {exitCode: 0, stdout: "GNU Wget"},
			},
			wantMethod: MethodWget,
			wantOK:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNomadService{execResponses: tt.responses}
			method, ok := ProbeHTTPCapability(mock, allocID, tt.task)
			if ok != tt.wantOK {
				t.Errorf("ProbeHTTPCapability() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && method != tt.wantMethod {
				t.Errorf("ProbeHTTPCapability() method = %v, want %v", method, tt.wantMethod)
			}
		})
	}
}

func TestResolveExecStrategy(t *testing.T) {
	allocID := "abcdef12-3456-7890-abcd-ef1234567890"

	tests := []struct {
		name       string
		taskOrder  []string
		responses  map[string]mockExecResponse
		wantTask   string
		wantMethod HTTPMethod
		wantErr    bool
	}{
		{
			name:      "sidecar has bash",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{
				"connect-proxy-web:bash": {exitCode: 0, stdout: "ok\n"},
			},
			wantTask:   "connect-proxy-web",
			wantMethod: MethodBashTCP,
		},
		{
			name:      "sidecar distroless, sibling has curl",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{
				"web:curl": {exitCode: 0, stdout: "curl 7.68.0"},
			},
			wantTask:   "web",
			wantMethod: MethodCurl,
		},
		{
			name:      "sidecar distroless, sibling has wget",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{
				"web:wget": {exitCode: 0, stdout: "GNU Wget 1.20"},
			},
			wantTask:   "web",
			wantMethod: MethodWget,
		},
		{
			name:      "sidecar distroless, sibling has node",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{
				"web:node": {exitCode: 0, stdout: "v18.0.0"},
			},
			wantTask:   "web",
			wantMethod: MethodNode,
		},
		{
			name:      "all tasks distroless",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{},
			wantErr:   true,
		},
		{
			name:      "prefers sidecar curl over sibling curl",
			taskOrder: []string{"connect-proxy-web", "web"},
			responses: map[string]mockExecResponse{
				"connect-proxy-web:curl": {exitCode: 0, stdout: "curl"},
				"web:curl":               {exitCode: 0, stdout: "curl"},
			},
			wantTask:   "connect-proxy-web",
			wantMethod: MethodCurl,
		},
		{
			name:      "three tasks, middle one works",
			taskOrder: []string{"connect-proxy-web", "web", "redis"},
			responses: map[string]mockExecResponse{
				"web:wget": {exitCode: 0, stdout: "GNU Wget"},
			},
			wantTask:   "web",
			wantMethod: MethodWget,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockNomadService{execResponses: tt.responses}
			strategy, err := ResolveExecStrategy(mock, allocID, tt.taskOrder)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveExecStrategy() expected error, got nil")
				}
				if !strings.Contains(err.Error(), "no HTTP tool found") {
					t.Errorf("error should mention 'no HTTP tool found', got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveExecStrategy() unexpected error: %v", err)
			}
			if strategy.Task != tt.wantTask {
				t.Errorf("strategy.Task = %q, want %q", strategy.Task, tt.wantTask)
			}
			if strategy.Method != tt.wantMethod {
				t.Errorf("strategy.Method = %v, want %v", strategy.Method, tt.wantMethod)
			}
		})
	}
}

func TestDecodeChunked(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple chunked",
			input: "5\r\nhello\r\n0\r\n\r\n",
			want:  "hello",
		},
		{
			name:  "multi chunk",
			input: "5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n",
			want:  "hello world",
		},
		{
			name:  "not chunked passthrough",
			input: `{"stats": "data"}`,
			want:  `{"stats": "data"}`,
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(decodeChunked([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("decodeChunked() = %q, want %q", got, tt.want)
			}
		})
	}
}
