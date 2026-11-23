// Package launcher holds what every sandbox backend gives the sandboxes it launches.
package launcher

import "time"

// Sandbox is the settings every backend's sandboxes share.
type Sandbox struct {
	// SandboxImage runs the worker; ToolImage, agentd-worker's own image, runs fetch and the sidecar.
	SandboxImage, ToolImage string
	// HandoffURL is the controller's GET /work-item base as sandboxes reach it, HandoffCA its CA in PEM.
	HandoffURL, HandoffCA string
	HandoffWait           time.Duration
}
