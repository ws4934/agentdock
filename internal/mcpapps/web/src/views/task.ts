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
import { taskID } from "../presentation";
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
  const steps = records(t.steps, 12);
  const id = taskID(t.id ?? d.task_id);
  const revision = text(t.revision ?? d.revision, 80);
  const current = object(t.current_step);
  const active =
    steps.find((s) => s.status === "in_progress" || s.status === "blocked") ||
    current;
  const total = Math.min(12, number(t.step_count) || steps.length),
    done = Math.min(
      total,
      typeof t.completed_step_count === "number"
        ? number(t.completed_step_count)
        : steps.filter((s) => s.status === "completed").length,
    );
  return {
    id: text(t.id ?? d.task_id) || "task",
    title: text(t.title) || tr(l, "任务进度", "Task progress"),
    task: id
      ? {
          id,
          canContinue:
            !t.archived_at && ["active", "blocked"].includes(text(t.status)),
        }
      : undefined,
    live: id
      ? {
          id,
          revision: /^tsk1:[a-f0-9]{64}$/.test(revision) ? revision : undefined,
          active: t.status === "active" && !t.archived_at,
        }
      : undefined,
    refresh: id ? { action: "snapshot", task_id: id } : undefined,
    summary:
      (t.status === "blocked" ? text(t.blocker) : "") ||
      text(t.summary ?? d.summary) ||
      text(active.title) ||
      tr(l, "等待下一步", "Waiting for the next step"),
    state: text(t.status ?? t.phase),
    tone: tone(t.status),
    progress: total ? { done, total } : undefined,
    metrics: [
      tr(
        l,
        "保存的任务进度，不代表进程仍在运行",
        "Saved progress, not proof of a running process",
      ),
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
