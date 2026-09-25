package task

// 公开请求类型与各自契约一一对应；统一领域命令不直接作为模型入口。
type LifecycleRequest struct {
	Action               string                 `json:"action"`
	TaskID               string                 `json:"task_id,omitempty"`
	Title                string                 `json:"title,omitempty"`
	Goal                 string                 `json:"goal,omitempty"`
	Project              string                 `json:"project,omitempty"`
	Device               string                 `json:"device,omitempty"`
	CompletionConditions []string               `json:"completion_conditions,omitempty"`
	Steps                []StepRequest          `json:"steps,omitempty"`
	Summary              string                 `json:"summary,omitempty"`
	TemplateID           string                 `json:"template_id,omitempty"`
	SourceTemplateIDs    []string               `json:"source_template_ids,omitempty"`
	LearningChecks       []LearningCheckRequest `json:"learning_checks,omitempty"`
}

func (r LifecycleRequest) Command() ManageRequest {
	return ManageRequest{Action: r.Action, TaskID: r.TaskID, Title: r.Title, Goal: r.Goal, Project: r.Project, Device: r.Device, CompletionConditions: r.CompletionConditions, Steps: r.Steps, Summary: r.Summary, TemplateID: r.TemplateID, SourceTemplateIDs: r.SourceTemplateIDs, LearningChecks: r.LearningChecks}
}

type UpdateRequest struct {
	Action           string    `json:"action"`
	TaskID           string    `json:"task_id,omitempty"`
	StepID           string    `json:"step_id,omitempty"`
	CompletedStepIDs *[]string `json:"completed_step_ids,omitempty"`
	CurrentStepID    string    `json:"current_step_id,omitempty"`
	Status           string    `json:"status,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Verified         []string  `json:"verified,omitempty"`
	Risks            []string  `json:"risks,omitempty"`
}

func (r UpdateRequest) Command() ManageRequest {
	return ManageRequest{Action: r.Action, TaskID: r.TaskID, StepID: r.StepID, CompletedStepIDs: r.CompletedStepIDs, CurrentStepID: r.CurrentStepID, Status: r.Status, Summary: r.Summary, Verified: r.Verified, Risks: r.Risks}
}
