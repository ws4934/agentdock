// 任意第三方结构化结果只能做有界预览；避免 stringify 巨型/循环对象阻塞主线程。
export function jsonPreview(value: unknown, budget = 18000): string {
  let nodes = 0;
  const seen = new WeakSet<object>();
  function visit(v: unknown, depth: number): unknown {
    if (++nodes > 1200 || depth > 8) return "[…]";
    if (typeof v === "string") return v.slice(0, 1000);
    if (v === null || typeof v !== "object") return v;
    if (seen.has(v)) return "[…]";
    seen.add(v);
    if (Array.isArray(v))
      return v.slice(0, 100).map((x) => visit(x, depth + 1));
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(v).slice(0, 100)) {
      Object.defineProperty(out, key, {
        value: visit((v as Record<string, unknown>)[key], depth + 1),
        enumerable: true,
      });
    }
    return out;
  }
  return (JSON.stringify(visit(value, 0), null, 2) ?? "").slice(0, budget);
}
