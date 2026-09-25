// 卡片只读更新调度器：一次请求、一个定时器；不执行任务，不扫描项目。
export type LivePhase =
  | "live"
  | "paused"
  | "hidden"
  | "stopped"
  | "error"
  | "expired"
  | "unavailable";
export interface LiveSnapshot {
  phase: LivePhase;
  lastConfirmed?: number;
}
export interface LiveClock {
  now(): number;
  set(fn: () => void, delay: number): unknown;
  clear(handle: unknown): void;
}
const clock: LiveClock = {
  now: () => Date.now(),
  set: (fn, delay) => window.setTimeout(fn, delay),
  clear: (handle) => window.clearTimeout(handle as number),
};
export const liveLeaseMS = 15 * 60 * 1000;

export class LiveProgress {
  private key = "";
  private active = false;
  private available = false;
  private visible = false;
  private paused = false;
  private failed = false;
  private ended = false;
  private busy = false;
  private epoch = 0;
  private started = 0;
  private unchanged = 0;
  private lastConfirmed?: number;
  private timer?: unknown;
  private controller?: AbortController;
  private published = "";
  constructor(
    private read: (signal: AbortSignal) => Promise<boolean>,
    private publish: (snapshot: LiveSnapshot) => void,
    private time: LiveClock = clock,
  ) {}
  configure(
    key: string,
    active: boolean,
    available: boolean,
    visible: boolean,
  ): void {
    if (this.ended) return;
    if (
      this.key === key &&
      this.active === active &&
      this.available === available &&
      this.visible === visible
    )
      return;
    const changed = this.key !== key;
    if (changed || !available || !visible) {
      this.epoch++;
      this.controller?.abort();
    }
    this.clearTimer();
    this.key = key;
    this.active = active;
    this.available = available;
    this.visible = visible;
    if (changed) {
      this.paused = false;
      this.failed = false;
      this.unchanged = 0;
      this.started = this.time.now();
      this.lastConfirmed = undefined;
    }
    this.emit();
    this.schedule(1000);
  }
  private phase(): LivePhase {
    if (!this.key || !this.available) return "unavailable";
    if (!this.active) return "stopped";
    if (this.failed) return "error";
    if (this.paused) return "paused";
    if (this.time.now() - this.started >= liveLeaseMS) return "expired";
    if (!this.visible) return "hidden";
    return "live";
  }
  private emit(): void {
    if (this.ended) return;
    const snapshot = { phase: this.phase(), lastConfirmed: this.lastConfirmed };
    const key = JSON.stringify(snapshot);
    if (key === this.published) return;
    this.published = key;
    this.publish(snapshot);
  }
  private clearTimer(): void {
    if (this.timer !== undefined) this.time.clear(this.timer);
    this.timer = undefined;
  }
  private schedule(delay: number): void {
    if (
      this.ended ||
      this.busy ||
      this.timer !== undefined ||
      this.phase() !== "live"
    )
      return;
    // 期限到达时只更新提示，不发出最后一轮请求。
    this.timer = this.time.set(
      () => {
        this.timer = undefined;
        if (this.phase() !== "live") {
          this.emit();
          return;
        }
        void this.run(false).catch(() => {});
      },
      Math.min(
        delay,
        Math.max(0, liveLeaseMS - (this.time.now() - this.started)),
      ),
    );
  }
  toggle(): void {
    if (this.ended || !this.key || !this.available || !this.active) return;
    const resume = this.paused || this.failed || this.phase() === "expired";
    this.paused = !resume;
    this.failed = false;
    if (resume) {
      this.started = this.time.now();
      this.unchanged = 0;
    } else {
      this.epoch++;
      this.controller?.abort();
    }
    this.clearTimer();
    this.emit();
    this.schedule(0);
  }
  refresh(): Promise<void> {
    return this.run(true);
  }
  invalidate(): void {
    if (this.ended || !this.busy) return;
    this.epoch++;
    this.controller?.abort();
  }
  private async run(manual: boolean): Promise<void> {
    if (this.ended || !this.key || !this.available)
      throw new Error("read-only refresh unavailable");
    if (this.busy) return;
    if (!manual && this.phase() !== "live") return;
    this.clearTimer();
    this.busy = true;
    const epoch = this.epoch,
      controller = new AbortController();
    this.controller = controller;
    try {
      const changed = await this.read(controller.signal);
      if (this.ended || epoch !== this.epoch || controller.signal.aborted)
        return;
      this.failed = false;
      this.lastConfirmed = this.time.now();
      this.unchanged = changed ? 0 : this.unchanged + 1;
    } catch (error) {
      if (this.ended || epoch !== this.epoch || controller.signal.aborted)
        return;
      // 包括宿主拒绝/协议错误：停止自动重试，保留上次观察并让用户手动恢复。
      this.failed = true;
      throw error;
    } finally {
      this.busy = false;
      if (this.controller === controller) this.controller = undefined;
      this.emit();
      this.schedule(
        this.unchanged >= 10 ? 15000 : this.unchanged >= 3 ? 10000 : 5000,
      );
    }
  }
  dispose(): void {
    this.ended = true;
    this.epoch++;
    this.clearTimer();
    this.controller?.abort();
    this.controller = undefined;
    this.key = "";
    this.published = "";
    this.lastConfirmed = undefined;
  }
}
