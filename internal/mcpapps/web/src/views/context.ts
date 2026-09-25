import {
  type Normalizer,
  type Data,
  type Group,
  object,
  records,
  length,
  group,
  text,
  tr,
} from "../model";
function groups(d: Data, l: "zh-CN" | "en"): Group[] {
  return [
    group("skills", tr(l, "技能", "Skills"), d.skills),
    group(
      "common",
      tr(l, "通用技能", "Common skills"),
      object(d.common_skills).items,
    ),
    group("mcp", "MCP", d.dynamic_mcp),
    group(
      "acp",
      "ACP",
      object(d.acp).enabled
        ? [
            {
              name: text(object(d.acp).agent) || "ACP",
              description: text(object(d.acp).description),
            },
          ]
        : [],
    ),
    group("workflow", tr(l, "工作流", "Workflows"), d.workflow_templates),
  ].filter((g) => g.rows.length);
}
export const normalize: Normalizer = (d, l) => {
  if (
    ![
      "skills",
      "nodes",
      "runtime",
      "dynamic_mcp",
      "common_skills",
      "workflow_templates",
    ].some((key) => Object.hasOwn(d, key))
  )
    throw new Error("Unexpected tool result shape");
  const nodes = records(d.nodes, 100);
  if (Array.isArray(d.nodes))
    return {
      id: "context-fleet",
      title: tr(l, "设备能力", "Device capabilities"),
      summary: tr(
        l,
        `${nodes.filter((n) => n.online === true).length} / ${length(d.nodes)} 台在线`,
        `${nodes.filter((n) => n.online === true).length} / ${length(d.nodes)} online`,
      ),
      empty: !nodes.length,
      tabs: nodes.map((n, i) => ({
        id: text(n.node_id ?? n.id) || String(i),
        title: text(n.name ?? n.display_name ?? n.node_id) || String(i + 1),
        summary: n.error
          ? tr(l, "设备暂不可用", "Device unavailable")
          : n.online === true
            ? tr(l, "在线", "Online")
            : tr(l, "离线", "Offline"),
        groups: groups(
          {
            ...object(n.context),
            workflow_templates: object(d.shared).workflow_templates,
          },
          l,
        ),
      })),
    };
  const skills = length(d.skills),
    mcps = length(d.dynamic_mcp),
    workflows = length(d.workflow_templates);
  const rt = object(d.runtime);
  return {
    id: "context",
    title: tr(l, "能力概览", "Capabilities"),
    summary: tr(
      l,
      `${skills} 项技能 · ${mcps} 个 MCP`,
      `${skills} skills · ${mcps} MCP`,
    ),
    metrics: [
      ...(workflows
        ? [tr(l, `${workflows} 个工作流`, `${workflows} workflows`)]
        : []),
      ...(object(d.recall).enabled
        ? [tr(l, "记忆已启用", "Memory enabled")]
        : []),
    ],
    groups: [
      ...groups(d, l),
      {
        id: "runtime",
        title: tr(l, "运行环境", "Runtime"),
        rows: Object.entries(rt)
          .filter(([key]) =>
            ["os", "arch", "version", "default_cwd"].includes(key),
          )
          .map(([key, value]) => ({
            id: key,
            title: key,
            description: text(value),
          })),
      },
    ],
    empty: !skills && !mcps && !workflows,
  };
};
