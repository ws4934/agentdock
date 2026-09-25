import {
  type Normalizer,
  object,
  records,
  text,
  group,
  tr,
  tone,
} from "../model";
export const normalize: Normalizer = (raw, l) => {
  if (
    ![
      "action",
      "state",
      "session",
      "sessions",
      "session_id",
      "agent",
      "authenticated",
    ].some((key) => Object.hasOwn(raw, key))
  )
    throw new Error("Unexpected tool result shape");
  const d = Object.keys(object(raw.state)).length ? object(raw.state) : raw,
    session = object(d.session);
  const messages = records(d.messages, 100).filter(
    (m) =>
      ["user", "assistant"].includes(text(m.role)) &&
      typeof m.content === "string",
  );
  return {
    id: text(session.id ?? d.session_id) || "acp",
    title:
      text(session.title ?? session.agent ?? object(d.agent).name) ||
      tr(l, "编程会话", "Coding session"),
    summary:
      text(d.message) ||
      text(messages.at(-1)?.content, 280) ||
      text(session.cwd) ||
      tr(l, "会话状态", "Session status"),
    state: text(session.status ?? d.status),
    tone: tone(session.status ?? d.status),
    groups: [
      group("sessions", tr(l, "会话", "Sessions"), d.sessions),
      {
        id: "messages",
        title: tr(l, "对话", "Conversation"),
        rows: messages.map((m, i) => ({
          id: String(i),
          title: tr(l, m.role === "user" ? "用户" : "助手", text(m.role)),
          description: text(m.content, 4000),
        })),
      },
    ].filter((g) => g.rows.length),
    empty: Array.isArray(d.sessions) && !d.sessions.length,
  };
};
