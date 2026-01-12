package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/markcampv/xDSnap/nomad"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func NewCaptureCommand(streams IOStreams) *cobra.Command {
	var allocID, taskName, namespace, serviceName string
	var endpoints []string
	var outputDir string
	var interval, duration, repeat int
	var enableTrace, tcpdumpEnabled bool

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}
	outputDir = cwd

	captureCmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture Envoy snapshots from a Consul Connect service mesh on Nomad",
		Long: `Capture Envoy configuration snapshots from Consul Connect sidecars running on Nomad.

This tool discovers Consul Connect allocations and captures:
- Envoy configuration dumps (/config_dump, /stats, /listeners, /clusters, /certs)
- Task logs (application and sidecar)
- Optional tcpdump packet captures

Environment variables:
  NOMAD_ADDR         Nomad API address (default: http://127.0.0.1:4646)
  NOMAD_TOKEN        Nomad ACL token (optional)
  CONSUL_HTTP_ADDR   Consul API address (default: http://127.0.0.1:8500)
  CONSUL_HTTP_TOKEN  Consul ACL token (optional)`,
		Run: func(cmd *cobra.Command, args []string) {
			// Create Nomad API service
			nomadService, err := nomad.NewNomadApiServiceFromEnv(namespace)
			if err != nil {
				log.Fatalf("Error creating Nomad client: %v", err)
			}

			// Determine which allocations to capture
			var allocsToCapture []nomad.AllocationInfo

			if allocID != "" {
				// Single allocation specified
				allocInfo, err := nomadService.GetAllocation(allocID)
				if err != nil {
					log.Fatalf("Error getting allocation %s: %v", allocID, err)
				}
				allocsToCapture = append(allocsToCapture, *allocInfo)
			} else if serviceName != "" {
				// Discover by service name
				allocs, err := nomadService.FindConnectAllocationsByService(namespace, serviceName)
				if err != nil {
					log.Fatalf("Error discovering allocations for service %s: %v", serviceName, err)
				}
				allocsToCapture = allocs
			} else {
				// Discover all Connect allocations
				allocs, err := nomadService.FindConnectAllocations(namespace)
				if err != nil {
					log.Fatalf("Error discovering Connect allocations: %v", err)
				}
				allocsToCapture = allocs
			}

			if len(allocsToCapture) == 0 {
				log.Println("No Consul Connect allocations found")
				return
			}

			log.Printf("Found %d allocation(s) to capture", len(allocsToCapture))
			for _, alloc := range allocsToCapture {
				log.Printf("  - %s (job: %s, group: %s, sidecar: %s)", alloc.ID[:8], alloc.JobID, alloc.TaskGroup, alloc.SidecarTask)
			}

			if interval < 5 {
				log.Fatalf("Interval must be at least 5 seconds")
			}

			if repeat > 0 {
				log.Printf("Starting snapshot capture with sleep=%ds repeat=%d trace=%v tcpdump=%v outputDir=%s",
					interval, repeat, enableTrace, tcpdumpEnabled, outputDir)
			} else {
				log.Printf("Starting snapshot capture with sleep=%ds duration=%ds trace=%v tcpdump=%v outputDir=%s",
					interval, duration, enableTrace, tcpdumpEnabled, outputDir)
			}

			captures := 0
			var startTime time.Time

			for {
				if repeat > 0 && captures >= repeat {
					log.Println("Repeat count reached, stopping capture")
					break
				}

				// Delay setting the duration timer until after first snapshot begins
				if repeat == 0 && duration > 0 && !startTime.IsZero() && time.Since(startTime) >= time.Duration(duration)*time.Second {
					log.Println("Duration ended, stopping capture")
					break
				}

				timestamp := time.Now().Format("20060102_150405")
				snapshotDir := fmt.Sprintf("%s/snapshot_%s", outputDir, timestamp)

				if err := os.MkdirAll(snapshotDir, 0755); err != nil {
					log.Printf("Failed to create snapshot directory: %v", err)
					continue
				}

				for _, alloc := range allocsToCapture {
					// Determine which task to use
					targetTask := taskName
					if targetTask == "" {
						// Use detected sidecar or first non-sidecar task
						if alloc.SidecarTask != "" {
							// Find a non-sidecar task for the application logs
							for _, t := range alloc.Tasks {
								if t != alloc.SidecarTask {
									targetTask = t
									break
								}
							}
						}
						if targetTask == "" && len(alloc.Tasks) > 0 {
							targetTask = alloc.Tasks[0]
						}
					}

					if alloc.SidecarTask == "" {
						log.Printf("No sidecar task found in allocation %s, skipping", alloc.ID[:8])
						continue
					}

					finalReset := repeat == 0 || captures == repeat-1

					log.Printf("Capturing allocation: %s | task: %s | sidecar: %s | trace: %v | tcpdump: %v",
						alloc.ID[:8], targetTask, alloc.SidecarTask, enableTrace, tcpdumpEnabled)

					snapshotConfig := SnapshotConfig{
						AllocID:           alloc.ID,
						AllocIP:           alloc.IP,
						TaskName:          targetTask,
						SidecarTask:       alloc.SidecarTask,
						Endpoints:         endpoints,
						OutputDir:         snapshotDir,
						ExtraLogs:         []string{alloc.SidecarTask},
						EnableTrace:       enableTrace,
						TcpdumpEnabled:    tcpdumpEnabled,
						Duration:          time.Duration(duration) * time.Second,
						SkipLogLevelReset: !finalReset,
					}

					// Start timer here *after* setup begins
					if repeat == 0 && duration > 0 && startTime.IsZero() {
						startTime = time.Now()
					}

					if err := CaptureSnapshot(nomadService, snapshotConfig); err != nil {
						log.Printf("Error capturing snapshot for allocation %s: %v", alloc.ID[:8], err)
					}
				}

				captures++

				if repeat > 0 && captures < repeat {
					log.Printf("Sleeping %ds before next snapshot (repeat mode)", interval)
					time.Sleep(time.Duration(interval) * time.Second)
				} else if repeat == 0 {
					time.Sleep(time.Duration(interval) * time.Second)
				}
			}
		},
	}

	// Nomad-specific flags
	captureCmd.Flags().StringVar(&allocID, "alloc", "", "Allocation ID (optional; defaults to all Connect allocations)")
	captureCmd.Flags().StringVar(&taskName, "task", "", "Task name for application logs (auto-detected if not specified)")
	captureCmd.Flags().StringVar(&serviceName, "service", "", "Consul service name to filter allocations")
	captureCmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Nomad namespace (optional)")

	// Capture options
	captureCmd.Flags().StringSliceVar(&endpoints, "endpoints", []string{}, "Envoy endpoints to capture")
	captureCmd.Flags().StringVar(&outputDir, "output-dir", outputDir, "Directory to save snapshots")
	captureCmd.Flags().IntVar(&interval, "sleep", 5, "Sleep duration between captures in seconds (minimum 5s)")
	captureCmd.Flags().IntVar(&duration, "duration", 60, "Total capture duration in seconds")
	captureCmd.Flags().IntVar(&repeat, "repeat", 0, "Number of snapshot repetitions (takes precedence over duration)")
	captureCmd.Flags().BoolVar(&enableTrace, "enable-trace", false, "Enable Envoy trace log level")
	captureCmd.Flags().BoolVar(&tcpdumpEnabled, "tcpdump", false, "Enable tcpdump capture (requires tcpdump in sidecar image)")

	_ = viper.BindEnv("namespace", "NOMAD_NAMESPACE")
	_ = viper.BindPFlag("namespace", captureCmd.Flags().Lookup("namespace"))

	return captureCmd
}

// IOStreams provides standard I/O streams
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// NewIOStreams returns IOStreams using os.Stdin, os.Stdout, os.Stderr
func NewIOStreams() IOStreams {
	return IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
}
