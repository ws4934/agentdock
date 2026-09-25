import {
  type Normalizer,
  object,
  records,
  text,
  number,
  group,
  tr,
  tone,
} from "../model";
export const normalize: Normalizer = (d, l) => {
  if (
    !["task", "task_summary", "tasks", "task_id"].some((key) =>
      Object.hasOwn(d, key),
    )
  )
    throw new Error("Unexpected tool result shape");
  if (Array.isArray(d.tasks))
    return {
      id: "task-list",
      title: tr(l, "任务列表", "Tasks"),
      summary: tr(
        l,
        `${number(d.count) || d.tasks.length} 个任务`,
        `${number(d.count) || d.tasks.length} tasks`,
      ),
      groups: [group("tasks", tr(l, "任务", "Tasks"), d.tasks)],
      empty: !d.tasks.length,
    };
  const t = Object.keys(object(d.task)).length
    ? object(d.task)
    : Object.keys(object(d.task_summary)).length
      ? object(d.task_summary)
      : d;
  const steps = records(t.steps);
  const current = object(t.current_step);
  const active =
    steps.find((s) => s.status === "in_progress" || s.status === "blocked") ||
    current;
  const total = number(t.step_count) || steps.length,
    done = Math.min(total, number(t.completed_step_count));
  return {
    id: text(t.id ?? d.task_id) || "task",
    title: text(t.title) || tr(l, "任务进度", "Task progress"),
    summary:
      text(t.summary ?? d.summary) ||
      text(active.title) ||
      tr(l, "等待下一步", "Waiting for the next step"),
    state: text(t.status ?? t.phase),
    tone: tone(t.status),
    progress: total ? { done, total } : undefined,
    metrics: [
      ...(active.title
        ? [tr(l, "当前：", "Current: ") + text(active.title, 200)]
        : []),
      ...(object(t.final_review).status
        ? [tr(l, "验收：", "Review: ") + text(object(t.final_review).status)]
        : []),
    ],
    groups: [
      group(
        "steps",
        tr(l, "步骤", "Steps"),
        steps.length ? steps : current.id ? [current] : [],
      ),
      group(
        "conditions",
        tr(l, "验收条件", "Acceptance"),
        t.condition_refs ?? t.conditions,
      ),
    ].filter((g) => g.rows.length),
  };
};
