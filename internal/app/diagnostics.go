package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/diagnostics"
	"github.com/uvwt/agentdock/internal/publicartifacts"
	"github.com/uvwt/agentdock/internal/securetunnel"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

type DiagnosticRequest struct {
	ProbePublic bool `json:"probe_public,omitempty"`
}

func diagnosticsContract(name string, _ config.Config) (ToolContract, bool) {
	if name != "runtime_diagnostics" && name != "diagnostic_export" {
		return ToolContract{}, false
	}
	in := toolcontract.InputObject(map[string]any{"probe_public": toolcontract.Boolean("Explicitly probe only the configured HTTPS public health URL, without credentials or redirects. Defaults to false.")})
	out := toolcontract.OutputObject(map[string]any{"diagnostics": toolcontract.OpenObject("Allowlisted build, readiness and trace metadata; never includes arguments, output bodies, environment or credentials."), "artifact_id": toolcontract.String("Private support bundle artifact ID."), "resource_uri": toolcontract.String("Resource link for the authorized MCP host."), "filename": toolcontract.String("Support bundle filename."), "mime_type": toolcontract.String("Support bundle media type.")})
	if name == "runtime_diagnostics" {
		toolcontract.Require(out, "diagnostics")
	} else {
		toolcontract.Require(out, "artifact_id", "resource_uri", "filename", "mime_type")
	}
	return ToolContract{InputSchema: in, OutputSchema: out}, true
}
func diagnosticsToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "runtime_diagnostics", Title: "Inspect connection layers", Contract: diagnosticsContract, Annotations: readOnlyToolAnnotations(true), Description: "Read safe Core/auth/transport/execution diagnostics. Tool completion is not proof of client receipt or UI rendering. No secrets, raw requests or outputs are included.", Handler: typedToolHandler("runtime_diagnostics", func(ctx context.Context, r *Runtime, request DiagnosticRequest) (Result, error) {
			return r.diagnosticResult(ctx, request.ProbePublic), nil
		})},
		{Name: "diagnostic_export", Title: "Export safe diagnostics", Contract: diagnosticsContract, Annotations: mutatingToolAnnotations(false, false), Description: "Create a private immutable support ZIP containing only fixed allowlisted diagnostic-report.json, diagnostic-report.md and activity-safe.json. Does not copy app data, project files, credentials or logs.", Handler: typedToolHandler("diagnostic_export", func(ctx context.Context, r *Runtime, request DiagnosticRequest) (Result, error) {
			return r.exportDiagnostics(ctx, request)
		})},
	}
}

type diagnosticLayer struct {
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
}
type diagnosticReport struct {
	SchemaVersion         int                        `json:"schema_version"`
	ObservedAt            time.Time                  `json:"observed_at"`
	Build                 buildinfo.Info             `json:"build"`
	Layers                map[string]diagnosticLayer `json:"layers"`
	TraceMode             string                     `json:"trace_mode"`
	Events                []diagnostics.Event        `json:"events"`
	ToolCount             int                        `json:"tool_count"`
	ToolDiscovery         toolDiscoveryContext       `json:"tool_discovery"`
	ManagedJobs           map[string]int             `json:"managed_jobs"`
	ManagedJobsPartial    bool                       `json:"managed_jobs_partial"`
	AuthConfigured        bool                       `json:"auth_configured"`
	MCPAppsEnabled        bool                       `json:"mcp_apps_enabled"`
	SecureTunnel          map[string]any             `json:"secure_tunnel"`
	SensitiveDataIncluded bool                       `json:"sensitive_data_included"`
}

func (r *Runtime) diagnosticResult(ctx context.Context, probe bool) Result {
	report := diagnosticReport{SchemaVersion: 1, ObservedAt: time.Now().UTC(), Build: buildinfo.Current(), TraceMode: "bounded_metadata_only", ToolCount: len(r.ToolNames()), AuthConfigured: r.cfg.AuthRequired(), MCPAppsEnabled: r.cfg.MCPAppsEnabled, ManagedJobs: map[string]int{}, Layers: map[string]diagnosticLayer{
		"core":              {Status: "ready", Reason: "observed_inside_current_Core"},
		"authentication":    {Status: "configured", Reason: "configuration_not_a_remote_authentication_probe"},
		"public_endpoint":   {Status: "not_probed", Reason: "explicit_probe_not_requested"},
		"tunnel":            {Status: "unknown", Reason: "Core_health_does_not_prove_tunnel_readiness"},
		"response_delivery": {Status: "unknown", Reason: "execution_completion_does_not_prove_client_receipt"},
		"ui_rendering":      {Status: "unknown", Reason: "host_mount_not_observable_from_Core"},
	}}
	report.ToolDiscovery = r.toolDiscoveryContext()
	if !r.cfg.AuthRequired() {
		report.Layers["authentication"] = diagnosticLayer{Status: "not_configured", Reason: "local_transport_trust_only"}
	}
	if r.cfg.OAuthServerURL == "" {
		report.Layers["public_endpoint"] = diagnosticLayer{Status: "not_configured", Reason: "no_public_HTTPS_origin"}
	} else if probe {
		report.Layers["public_endpoint"] = probeConfiguredEndpoint(ctx, r.cfg.OAuthServerURL)
	}
	if r.traces != nil {
		report.Events = r.traces.Snapshot()
	}
	if jobs, err := r.command.Jobs(); err != nil {
		report.Layers["managed_jobs"] = diagnosticLayer{Status: "unknown", Reason: "job_store_unavailable"}
	} else {
		records, partial, err := jobs.List("", 200)
		report.ManagedJobsPartial = partial
		if err != nil {
			report.Layers["managed_jobs"] = diagnosticLayer{Status: "unknown", Reason: "job_state_unreadable"}
		} else {
			report.Layers["managed_jobs"] = diagnosticLayer{Status: "available", Reason: "observed_receipts_not_process_survival_guarantee"}
			allowed := map[string]bool{"starting": true, "preparing": true, "running": true, "succeeded": true, "failed": true, "cancelled": true, "timed_out": true, "outcome_unknown": true, "launch_failed": true, "launch_expired": true, "validation_not_started": true}
			for _, j := range records {
				state := j.Status
				if !allowed[state] {
					state = "unknown"
				}
				report.ManagedJobs[state]++
			}
		}
	}
	report.SecureTunnel = securetunnel.New(r.cfg.AgentDockHome, securetunnel.ClientEnvironment).Status(ctx, probe)
	return Result{"diagnostics": report}
}
func probeConfiguredEndpoint(ctx context.Context, origin string) diagnosticLayer {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return diagnosticLayer{Status: "invalid_configuration", Reason: "public_origin_must_be_HTTPS"}
	}
	parsed.Path = "/healthz"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return diagnosticLayer{Status: "unknown", Reason: "probe_request_failed"}
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	response, err := client.Do(req)
	if err != nil {
		return diagnosticLayer{Status: "unavailable", Reason: "network_probe_failed", LatencyMS: time.Since(start).Milliseconds()}
	}
	defer response.Body.Close()
	layer := diagnosticLayer{Status: "unavailable", Reason: "HTTP_health_check_failed", HTTPStatus: response.StatusCode, LatencyMS: time.Since(start).Milliseconds()}
	if response.StatusCode == 200 {
		var health struct {
			OK bool `json:"ok"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health) == nil && health.OK {
			layer.Status = "reachable"
			layer.Reason = "public_Core_health_observed_not_UI_proof"
		}
	}
	return layer
}
func (r *Runtime) RuntimeDiagnostics(ctx context.Context) (Result, error) {
	return r.diagnosticResult(ctx, false), nil
}
func (r *Runtime) exportDiagnostics(ctx context.Context, request DiagnosticRequest) (Result, error) {
	report := r.diagnosticResult(ctx, request.ProbePublic)["diagnostics"].(diagnosticReport)
	full, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	events, err := json.MarshalIndent(report.Events, "", "  ")
	if err != nil {
		return nil, err
	}
	description := fmt.Sprintf("# AgentDock diagnostics\n\nVersion: %s\nCommit: %s\nObserved: %s\n\nThis bundle contains only allowlisted metadata. RPC success is not proof of response receipt, tests passing or UI rendering. No configuration values, credentials, project files, command lines or request/result bodies are included.\n", report.Build.Version, report.Build.Commit, report.ObservedAt.Format(time.RFC3339))
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"diagnostic-report.json", full}, {"diagnostic-report.md", []byte(description)}, {"activity-safe.json", events}} {
		file, err := archive.Create(entry.name)
		if err != nil {
			return nil, err
		}
		if _, err = file.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	store := publicartifacts.New(r.cfg.AgentDockHome, "", r.cfg.Port)
	published, err := store.PublishBytes(publicartifacts.PublishBytesRequest{Private: true, Filename: "AgentDock-support.zip", MimeType: "application/zip", Data: buffer.Bytes()})
	if err != nil {
		return nil, err
	}
	return Result{"artifact_id": published.ArtifactID, "filename": published.Filename, "mime_type": published.MimeType, "size_bytes": published.Size, "sha256": published.SHA256, "expires_at": published.ExpiresAt, "delivery": "private", "resource_uri": publicartifacts.ResourceURI(published.ArtifactID)}, nil
}
