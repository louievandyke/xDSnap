package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/markcampv/xDSnap/nomad"
)

type SnapshotConfig struct {
	AllocID           string
	AllocIP           string
	TaskName          string
	SidecarTask       string
	Endpoints         []string
	OutputDir         string
	ExtraLogs         []string
	Duration          time.Duration
	EnableTrace       bool
	TcpdumpEnabled    bool
	SkipLogLevelReset bool
}

var DefaultEndpoints = []string{"/stats", "/config_dump", "/listeners", "/clusters", "/certs"}

func CaptureSnapshot(nomadService nomad.NomadApiService, config SnapshotConfig) error {
	if len(config.Endpoints) == 0 {
		config.Endpoints = DefaultEndpoints
	}

	log.Printf("CaptureSnapshot called with Alloc=%s Task=%s Sidecar=%s EnableTrace=%v",
		config.AllocID[:8], config.TaskName, config.SidecarTask, config.EnableTrace)

	tempDir, err := os.MkdirTemp("", config.AllocID[:8])
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Stream logs from app task + any extras (e.g., sidecar)
	logResults := make(chan struct{}, len(config.ExtraLogs)+1)

	// Collect unique tasks to get logs from
	tasksToLog := []string{config.TaskName}
	for _, t := range config.ExtraLogs {
		if t != "" && t != config.TaskName {
			tasksToLog = append(tasksToLog, t)
		}
	}

	for _, task := range tasksToLog {
		if task == "" {
			logResults <- struct{}{}
			continue
		}
		task := task
		go func() {
			log.Printf("Starting log stream for task %s", task)
			logBytes, err := streamLogsWithTimeout(nomadService, config.AllocID, task, config.Duration+10*time.Second)
			if err != nil {
				log.Printf("Failed to stream logs for task %s: %v", task, err)
			} else {
				logsPath := filepath.Join(tempDir, fmt.Sprintf("%s-logs.txt", task))
				if err := os.WriteFile(logsPath, logBytes, 0644); err != nil {
					log.Printf("Failed to write logs for task %s: %v", task, err)
				}
			}
			logResults <- struct{}{}
		}()
	}

	// --- Set Envoy log level via exec ---
	logLevel := "debug"
	if config.EnableTrace {
		logLevel = "trace"
	}
	log.Printf("Setting Envoy log level to '%s' via nomad exec", logLevel)

	if err := setEnvoyLogLevel(nomadService, config, logLevel); err != nil {
		log.Printf("Failed to set log level: %v", err)
	}

	// --- Optional tcpdump capture ---
	if config.TcpdumpEnabled {
		log.Printf("Starting tcpdump capture...")
		pcapData, err := captureTcpdump(nomadService, config)
		if err != nil {
			log.Printf("Failed to capture tcpdump: %v", err)
		} else if len(pcapData) > 0 {
			pcapPath := filepath.Join(tempDir, "capture.pcap")
			if err := os.WriteFile(pcapPath, pcapData, 0644); err != nil {
				log.Printf("Failed to write pcap file: %v", err)
			} else {
				log.Printf("Saved .pcap file: %s", pcapPath)
			}
		}
	}

	// --- Envoy admin endpoints ---
	for _, endpoint := range config.Endpoints {
		data, err := fetchEnvoyEndpoint(nomadService, config, endpoint)
		if err != nil {
			log.Printf("Error capturing %s: %v", endpoint, err)
			continue
		}
		if len(data) == 0 {
			log.Printf("Warning: No data received from endpoint %s for alloc %s", endpoint, config.AllocID[:8])
			continue
		}
		filePath := filepath.Join(tempDir, fmt.Sprintf("%s.json", strings.TrimPrefix(endpoint, "/")))
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			log.Printf("Failed to write data for %s: %v", endpoint, err)
		} else {
			fmt.Printf("Captured %s for %s and saved to %s\n", endpoint, config.AllocID[:8], filePath)
		}
	}

	// Wait for all log streams to finish
	for i := 0; i < len(tasksToLog); i++ {
		<-logResults
	}

	// Bundle snapshot
	tarFilePath := filepath.Join(config.OutputDir, fmt.Sprintf("%s_snapshot.tar.gz", config.AllocID[:8]))
	if err := createTarGz(tarFilePath, tempDir); err != nil {
		return fmt.Errorf("failed to create tar.gz file: %w", err)
	}
	fmt.Printf("Snapshot for %s saved as %s\n", config.AllocID[:8], tarFilePath)

	// Reset log level
	if !config.SkipLogLevelReset {
		log.Printf("Resetting Envoy log level back to 'info' on alloc: %s", config.AllocID[:8])
		if err := setEnvoyLogLevel(nomadService, config, "info"); err != nil {
			log.Printf("Failed to reset log level to info: %v", err)
		}
	}

	return nil
}

func streamLogsWithTimeout(nomadService nomad.NomadApiService, allocID, task string, duration time.Duration) ([]byte, error) {
	var logsBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Stream both stdout and stderr
	done := make(chan error, 2)

	go func() {
		done <- nomadService.FetchTaskLogs(ctx, allocID, task, "stdout", true, &logsBuf)
	}()

	go func() {
		var stderrBuf bytes.Buffer
		err := nomadService.FetchTaskLogs(ctx, allocID, task, "stderr", true, &stderrBuf)
		if err == nil && stderrBuf.Len() > 0 {
			logsBuf.WriteString("\n--- STDERR ---\n")
			logsBuf.Write(stderrBuf.Bytes())
		}
		done <- err
	}()

	// Wait for context or completion
	select {
	case <-ctx.Done():
		return logsBuf.Bytes(), nil
	case err := <-done:
		// Wait for the second goroutine too
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return logsBuf.Bytes(), err
	}
}

func setEnvoyLogLevel(nomadService nomad.NomadApiService, config SnapshotConfig, level string) error {
	// Try direct HTTP first if we have an IP
	if config.AllocIP != "" {
		path := fmt.Sprintf("/logging?level=%s", level)
		err := nomadService.EnvoyAdminPOST(config.AllocIP, nomad.EnvoyAdminPort, path)
		if err == nil {
			return nil
		}
		log.Printf("Direct HTTP failed, falling back to exec: %v", err)
	}

	// Fallback to exec
	path := fmt.Sprintf("/logging?level=%s", level)
	return nomadService.EnvoyAdminPOSTViaExec(config.AllocID, config.SidecarTask, nomad.EnvoyAdminPort, path)
}

func fetchEnvoyEndpoint(nomadService nomad.NomadApiService, config SnapshotConfig, endpoint string) ([]byte, error) {
	const maxRetries = 3
	const retryDelay = 2 * time.Second

	// Try direct HTTP first if we have an IP
	if config.AllocIP != "" {
		for i := 0; i < maxRetries; i++ {
			data, err := nomadService.EnvoyAdminGET(config.AllocIP, nomad.EnvoyAdminPort, endpoint)
			if err == nil && len(data) > 0 {
				return data, nil
			}
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
			}
		}
		log.Printf("Direct HTTP failed for %s, falling back to exec", endpoint)
	}

	// Fallback to exec
	return nomadService.EnvoyAdminGETViaExec(config.AllocID, config.SidecarTask, nomad.EnvoyAdminPort, endpoint)
}

func captureTcpdump(nomadService nomad.NomadApiService, config SnapshotConfig) ([]byte, error) {
	// Run tcpdump via exec in the sidecar task
	// This requires tcpdump to be available in the sidecar image
	durationSecs := int(config.Duration.Seconds())
	if durationSecs < 5 {
		durationSecs = 5
	}

	// Capture traffic and base64 encode it for transport
	cmd := []string{
		"sh", "-c",
		fmt.Sprintf("timeout %d tcpdump -i any -s0 -w - 2>/dev/null | base64", durationSecs),
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	log.Printf("Running tcpdump for %d seconds in task %s", durationSecs, config.SidecarTask)

	_, err := nomadService.ExecuteCommandWithStderr(config.AllocID, config.SidecarTask, cmd, &stdout, &stderr)
	if err != nil {
		// Check if tcpdump is not available
		if strings.Contains(stderr.String(), "not found") || strings.Contains(err.Error(), "not found") {
			return nil, fmt.Errorf("tcpdump not available in sidecar image")
		}
		return nil, fmt.Errorf("tcpdump failed: %w (stderr: %s)", err, stderr.String())
	}

	if stdout.Len() == 0 {
		log.Printf("No tcpdump data captured")
		return nil, nil
	}

	// Decode base64
	raw := stdout.String()
	clean := regexp.MustCompile(`[^A-Za-z0-9+/=]`).ReplaceAllString(strings.TrimSpace(raw), "")
	if clean == "" {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 tcpdump stream: %w", err)
	}

	return data, nil
}

func createTarGz(outputFile string, sourceDir string) error {
	tarFile, err := os.Create(outputFile)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	gzipWriter := gzip.NewWriter(tarFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	err = filepath.Walk(sourceDir, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(sourceDir, file)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(fi, relPath)
		if err != nil {
			return err
		}
		header.Name = relPath
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tarWriter, f)
		return err
	})

	return err
}
