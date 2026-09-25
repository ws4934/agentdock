import {
  type Normalizer,
  type Data,
  object,
  records,
  text,
  number,
  group,
  tr,
} from "../model";
import { taskID } from "../presentation";

export const normalize: Normalizer = (data, l) => {
  const p = object(data.work_result);
  if (!p.task || !p.source) throw new Error("Invalid work result");
  const task = object(p.task),
    source = object(p.source),
    frozen = p.frozen === true;
  const jobs = records(p.jobs, 32),
    artifacts = records(p.artifacts, 8);
  const freshness = (v: unknown) =>
    ({
      current: tr(l, "对应当前源码", "Current source"),
      stale: tr(l, "已过期", "Stale"),
      unproven: tr(l, "源码未能确认", "Source unproven"),
      not_available: tr(l, "没有验证证据", "No validation evidence"),
    })[text(v)] ?? text(v);
  const labels: Record<string, string> = {
    not_verified: tr(l, "尚未验证", "Not verified"),
    current_failures_present: tr(
      l,
      "当前仍有验证失败",
      "Current validation failures",
    ),
    current_evidence_available_not_coverage_guarantee: tr(
      l,
      "有当前验证证据，不代表覆盖充分",
      "Current evidence; not a coverage guarantee",
    ),
    inconclusive: tr(l, "验证结论不足", "Inconclusive"),
    no_current_validation_proof: tr(
      l,
      "没有适用于当前源码的验证证据",
      "No current source validation proof",
    ),
  };
  const rows = jobs.map((item, i) => {
    const job = object(item.job),
      e = object(job.evidence);
    return {
      id: text(job.job_id) || String(i),
      title:
        text(job.title) || text(e.adapter) || tr(l, "执行记录", "Execution"),
      state: text(job.status),
      description: [
        freshness(item.freshness),
        text(e.reason),
        e.tests !== undefined
          ? tr(
              l,
              `通过 ${number(e.passed)} · 失败 ${number(e.failed)} · 跳过 ${number(e.skipped)}`,
              `${number(e.passed)} passed · ${number(e.failed)} failed · ${number(e.skipped)} skipped`,
            )
          : "",
      ]
        .filter(Boolean)
        .join(" · "),
    };
  });
  const sourcePaths = Array.isArray(source.scope)
    ? source.scope
        .filter((s): s is string => typeof s === "string")
        .slice(0, 128)
    : [];
  const selection = object(p.selection);
  const tokens = (v: unknown, n: number) =>
    Array.isArray(v)
      ? v
          .filter((s): s is string => typeof s === "string")
          .slice(0, n)
          .map((s) => text(s, 4096))
      : [];
  const refresh: Data = {
    task_id: text(task.id, 128),
    workdir: text(p.workdir, 4096),
    source_paths: tokens(selection.source_paths ?? sourcePaths, 128),
    job_ids: tokens(selection.job_ids, 32),
    artifact_ids: tokens(selection.artifact_ids, 8),
  };

  return {
    id: text(p.result_id) || text(task.id) || "work-result",
    title: text(task.title, 400) || tr(l, "工作结果", "Work result"),
    summary:
      labels[text(p.validation)] || tr(l, "验证状态未知", "Validation unknown"),
    tone: p.validation === "current_failures_present" ? "error" : "neutral",
    task: taskID(task.id) ? { id: taskID(task.id)!, canContinue: !frozen && ["active", "blocked"].includes(text(task.status)) } : undefined,
    state: text(task.status),
    progress: Array.isArray(task.steps) && task.steps.length > 0 ? {
      total: Math.min(task.steps.length, 12),
      done: records(task.steps, 12).filter(step => step.status === "completed").length,
    } : undefined,
    metrics: [
      frozen
        ? tr(l, "冻结交付 · 历史快照", "Frozen delivery · historical snapshot")
        : tr(l, "当前工作区观察", "Live workspace observation"),
      tr(l, "观察时间：", "Observed: ") + text(p.observed_at, 80),
      ...(source.head ? [text(source.head, 12)] : []),
    ],
    groups: [
      group("steps", tr(l, "任务检查点", "Task checkpoints"), records(task.steps, 12)),
      {
        id: "validation",
        title: tr(l, "机器验证证据", "Machine evidence"),
        rows,
      },
      group(
        "changes",
        tr(l, "变更路径", "Changed paths"),
        (Array.isArray(p.changes) ? p.changes : [])
          .slice(0, 128)
          .map((v) => ({ title: text(v, 1024) })),
      ),
      group(
        "artifacts",
        tr(l, "交付文件", "Artifacts"),
        artifacts.map((a) => ({
          id: a.artifact_id,
          title: a.filename,
          description: `SHA-256 ${text(a.sha256, 64)}`,
        })),
      ),
      {
        id: "review",
        title: tr(l, "人工 / 模型验收", "Operator / model review"),
        rows: [
          {
            id: "assertion",
            title:
              text(object(task.final_review).status) ||
              tr(l, "未提交", "Not recorded"),
            description: tr(
              l,
              "任务验收是人工或模型声明，不代替上方执行与源码证据。",
              "Task review is an operator/model assertion, not execution or source proof.",
            ),
          },
        ],
      },
    ].filter((g) => g.rows.length),
    truncated: p.jobs_partial === true || p.changes_partial === true,
    refresh:
      !frozen && refresh.task_id && refresh.workdir ? refresh : undefined,
  };
};
