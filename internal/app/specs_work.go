package app

import (
	"context"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/workresult"
)

func workToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, workresult.InputSchema, workresult.OutputSchema)
}
func workToolSpecs() []ToolSpec {
	return []ToolSpec{
		{Name: "work_result_show", Contract: workToolContract, Title: "Show work result", Description: "Explicitly show one interactive work-result card. Use work_result_read for intermediate checks without rendering. Call show only for a requested visual summary or final handoff; never after every checkpoint. Reads current or frozen evidence without executing jobs.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("work_result_show", readWorkResult)},
		{Name: "work_result_read", Contract: workToolContract, Title: "Read work result", Description: "Read current or frozen task/project evidence as data only, without rendering a card or rerunning commands. Prefer this for intermediate checks; use work_result_show only for an explicit visual summary. Select exact job IDs when listing is partial.", Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("work_result_read", readWorkResult)},
		{Name: "work_result_freeze", Contract: workToolContract, Title: "Freeze a work delivery", Description: "Seal a completed task's exact source observation, terminal validation receipts and immutable artifact references and automatically show its delivery card; no extra work_result_show call is needed. Requires the prior observed source revision; does not mark unverifiable work as tested.", Annotations: mutatingToolAnnotations(false, false), Handler: typedToolHandler("work_result_freeze", func(ctx context.Context, r *Runtime, request workresult.Request) (Result, error) {
			p, err := r.ws.ResolveExisting(request.Workdir)
			if err != nil {
				return nil, err
			}
			request.Workdir = p.Abs
			result, err := r.workResults.Freeze(ctx, request)
			if err != nil {
				return nil, err
			}
			return Result{"work_result": result, "result_id": result.ResultID, "frozen": true, "observation_only": true}, nil
		})},
	}
}

func readWorkResult(ctx context.Context, r *Runtime, request workresult.Request) (Result, error) {
	if request.ResultID == "" {
		p, err := r.ws.ResolveExisting(request.Workdir)
		if err != nil {
			return nil, err
		}
		request.Workdir = p.Abs
	}
	p, err := r.workResults.Read(ctx, request)
	if err != nil {
		return nil, err
	}
	return Result{"work_result": p, "result_id": p.ResultID, "frozen": p.Frozen, "observation_only": true}, nil
}
