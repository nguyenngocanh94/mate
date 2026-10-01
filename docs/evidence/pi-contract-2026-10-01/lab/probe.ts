// Lab-only probe: logs every lifecycle event pi emits, with payload shape,
// to the JSONL file named by PIPROBE_LOG. Never answers or blocks anything.
import { appendFileSync } from "node:fs";

const LOG = process.env.PIPROBE_LOG || "/dev/null";
const MARK = process.env.PIPROBE_MARK || "";

function count(hay: string, needle: string): number {
  if (!needle) return 0;
  return hay.split(needle).length - 1;
}

function write(rec: Record<string, unknown>) {
  try {
    appendFileSync(LOG, JSON.stringify({ t: new Date().toISOString(), ...rec }) + "\n");
  } catch {}
}

function shape(ev: any): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(ev ?? {})) {
    out[k] = Array.isArray(v) ? `array(${v.length})` : v === null ? "null" : typeof v;
  }
  return out;
}

function sess(ctx: any) {
  try {
    const sm = ctx?.sessionManager;
    return {
      sessionId: sm?.getSessionId?.(),
      sessionFile: sm?.getSessionFile?.(),
      sessionDir: sm?.getSessionDir?.(),
      cwd: ctx?.cwd,
      model: ctx?.model ? `${ctx.model.provider}/${ctx.model.id}` : undefined,
      thinkingLevel: ctx?.thinkingLevel,
      trusted: ctx?.isProjectTrusted?.(),
      idle: ctx?.isIdle?.(),
    };
  } catch (e) {
    return { err: String(e) };
  }
}

const EVENTS = [
  "session_start", "session_shutdown", "session_info_changed",
  "session_before_switch", "session_before_fork", "session_compact",
  "input", "before_agent_start", "agent_start", "turn_start", "turn_end",
  "agent_before_settle", "agent_end", "agent_settled",
  "message_start", "message_end",
  "tool_call", "tool_execution_start", "tool_execution_end", "tool_result",
  "model_select", "thinking_level_select", "ui_prompt_start", "ui_prompt_end",
  "resources_discover", "user_bash",
];

export default function (pi: any) {
  write({ ev: "factory", pid: process.pid, argv: process.argv.slice(2) });
  for (const name of EVENTS) {
    pi.on(name, (event: any, ctx: any) => {
      const rec: Record<string, unknown> = { ev: name, shape: shape(event), ctx: sess(ctx) };
      if (name === "session_start") {
        try {
          const sp: string = ctx.getSystemPrompt();
          rec.systemPromptLen = sp.length;
          const needles = (process.env.PIPROBE_NEEDLES || "MARK-AGENTS,MARK-CLAUDE,MARK-APPEND,MARK-OVERRIDE,MARK-PARENT,MARK-BIG-HEAD,MARK-BIG-TAIL").split(",");
          rec.needles = Object.fromEntries(needles.map((n) => [n, count(sp, n)]));
        } catch (e) {
          rec.systemPromptErr = String(e);
        }
      }
      if (name === "session_start" || name === "session_shutdown") {
        rec.reason = event.reason;
        rec.previousSessionFile = event.previousSessionFile;
        rec.targetSessionFile = event.targetSessionFile;
      }
      if (name === "input") {
        rec.text = event.text;
        rec.source = event.source;
        rec.streamingBehavior = event.streamingBehavior;
      }
      if (name === "before_agent_start") {
        const o = event.systemPromptOptions ?? {};
        const sp: string = event.systemPrompt ?? "";
        rec.prompt = event.prompt;
        rec.systemPromptLen = sp.length;
        rec.markCount = count(sp, MARK);
        rec.contextFiles = (o.contextFiles ?? []).map((f: any) => ({ path: f.path, len: (f.content ?? "").length, marks: count(f.content ?? "", MARK) }));
        rec.appendLen = (o.appendSystemPrompt ?? "").length;
        rec.appendMarks = count(o.appendSystemPrompt ?? "", MARK);
        rec.sectionKeys = Object.keys(o.sections ?? {});
      }
      if (name === "message_end") {
        const m = event.message ?? {};
        rec.role = m.role;
        rec.usage = m.usage;
        rec.stopReason = m.stopReason;
      }
      if (name === "tool_call") {
        rec.toolName = event.toolName;
        rec.input = event.input;
      }
      if (name === "agent_end") rec.messageCount = (event.messages ?? []).length;
      if (name === "thinking_level_select" || name === "model_select") rec.payload = event;
      if (name === "ui_prompt_start") { rec.kind = event.kind; rec.title = event.title; }
      write(rec);
    });
  }
  // Injection test: a hook-equivalent that adds context for this turn only
  // when the prompt asks for the secret word.
  pi.on("before_agent_start", (event: any) => {
    if (typeof event.prompt === "string" && event.prompt.includes("secret word")) {
      write({ ev: "inject", via: "before_agent_start.message" });
      return { message: { customType: "piprobe", content: "The secret word is ZEBRA-7731.", display: false } };
    }
    return undefined;
  });
  pi.on("project_trust", (event: any, ctx: any) => {
    write({ ev: "project_trust", cwd: event.cwd, mode: ctx?.mode, hasUI: ctx?.hasUI });
    return { trusted: "undecided" };
  });
}
