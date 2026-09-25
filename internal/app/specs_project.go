package app

import (
	"context"
	"errors"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/semantic"
	"github.com/uvwt/agentdock/internal/worktree"
)

func semanticContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, semantic.InputSchema, semantic.OutputSchema)
}
func worktreeContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, worktree.InputSchema, worktree.OutputSchema)
}
func projectToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "code_navigate", Contract: semanticContract, Title: "Read Go semantic information", Description: "Read-only gopls navigation, symbols, references, hover and diagnostics. status does not start a server. Queries use a fresh bounded server, no dependency/toolchain downloads, and omit locations outside the selected project. Positions use 1-based line and zero-based UTF-16 character. No edits or executeCommand are exposed.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("code_navigate", func(ctx context.Context, r *Runtime, request semantic.Request) (Result, error) {
			if request.Action != "status" {
				p, err := r.ws.ResolveExisting(request.Project)
				if err != nil {
					return nil, err
				}
				request.Project = p.Abs
			}
			result, err := r.navigation.Query(ctx, request)
			return Result(result), err
		})},
		{Name: "worktree_manage", Contract: worktreeContract, Title: "Manage isolated Git workspaces", Description: "Explicitly create or inspect managed detached Git worktrees. Requires an exact existing commit. Does not copy dirty changes, change the user's active directory, fetch, merge or delete worktrees. Worktrees isolate edits, not process permissions.", Annotations: mutatingToolAnnotations(false, false), Handler: typedToolHandler("worktree_manage", func(ctx context.Context, r *Runtime, request worktree.Request) (Result, error) {
			switch request.Action {
			case "list":
				records, partial, err := r.worktrees.List(ctx)
				return Result{"worktrees": records, "truncated": partial}, err
			case "status":
				record, err := r.worktrees.Status(ctx, request.WorktreeID)
				return Result{"worktree": record}, err
			case "create":
				p, err := r.ws.ResolveExisting(request.Project)
				if err != nil {
					return nil, err
				}
				request.Project = p.Abs
				record, err := r.worktrees.Create(ctx, request)
				if err != nil {
					return nil, err
				}
				return Result{"worktree": record}, nil
			default:
				return nil, errors.New("unsupported worktree action")
			}
		})},
	}
}
