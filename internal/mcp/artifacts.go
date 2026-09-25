package mcp

import (
	"context"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func (s *Server) registerArtifactResources() {
	store := publicartifacts.New(s.cfg.AgentDockHome, s.cfg.OAuthServerURL, s.cfg.Port)
	s.sdk.AddResourceTemplate(&mcpsdk.ResourceTemplate{Name: "AgentDock artifact", Title: "Immutable file delivery", URITemplate: publicartifacts.ResourcePrefix + "{artifact_id}", Description: "Read immutable artifact bytes through the authorized MCP connection (or explicitly trusted local stdio). Maximum 64 MiB. The host should materialize the resource; models should not fetch repeated Base64 chunks."}, func(ctx context.Context, r *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		if r == nil || r.Params == nil {
			return nil, mcpsdk.ResourceNotFoundError("")
		}
		id, ok := publicartifacts.ResourceID(r.Params.URI)
		if !ok {
			return nil, mcpsdk.ResourceNotFoundError(r.Params.URI)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		meta, data, err := store.Read(id, publicartifacts.MaxResourceBytes)
		if err != nil {
			return nil, mcpsdk.ResourceNotFoundError(r.Params.URI)
		}
		return &mcpsdk.ReadResourceResult{Contents: []*mcpsdk.ResourceContents{{URI: r.Params.URI, MIMEType: meta.MimeType, Blob: data, Meta: mcpsdk.Meta{"sha256": meta.SHA256, "filename": meta.Filename, "size_bytes": meta.Size}}}}, nil
	})
}
