import { spawnSync } from "node:child_process";

export default function (pi) {
  const termyardBin = process.env.TERMYARD_BIN || "__TERMYARD_BIN__";
  const pendingPaths = new Map();

  const safeString = (value, fallback = "") => {
    if (typeof value === "string") return value;
    if (value === null || value === undefined) return fallback;
    return String(value);
  };

  const getEvent = (event) => event || {};
  const getToolName = (event) => {
    const data = getEvent(event);
    return safeString(data.tool_name || data.toolName);
  };

  const notify = (status, message, extraArgs = []) => {
    try {
      spawnSync(termyardBin, ["notify", "-t", "pi", "-s", status, "-m", message, ...extraArgs], { stdio: "ignore" });
    } catch (e) {
      // Never crash Pi
    }
  };

  const fireStdin = (payload) => {
    try {
      spawnSync(termyardBin, ["notify", "-t", "pi", "--stdin"], {
        input: payload,
        stdio: ["pipe", "ignore", "ignore"],
        encoding: "utf8",
      });
    } catch (e) {
      // Never crash Pi
    }
  };

  // Pass tool name via stdin JSON so termyard maps it to an activity label
  // (e.g. "bash" → "running commands", "read" → "reading files")
  const notifyWithToolName = (toolName) => {
    fireStdin(JSON.stringify({ hook_event_name: "PreToolUse", tool_name: safeString(toolName) }));
  };

  // Capture current git branch as a short task label
  const getGitBranch = (cwd) => {
    try {
      const result = spawnSync("git", ["branch", "--show-current"], {
        cwd: cwd || process.cwd(),
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      });
      const branch = safeString(result && result.stdout).trim();
      return branch.slice(0, 40);
    } catch (e) {
      return "";
    }
  };

  // Capture user prompt BEFORE agent starts (prompt field only exists here)
  let currentUserPrompt = "";

  pi.on("before_agent_start", async (event, ctx) => {
    currentUserPrompt = "";
    const extraArgs = [];
    const sessionID = safeString(ctx && ctx.sessionId);
    if (sessionID) {
      extraArgs.push("--agent-session-id", sessionID);
    }
    // Task = first user prompt (primary), git branch (fallback only)
    const data = getEvent(event);
    const prompt = safeString(data.prompt);
    if (prompt) {
      const truncated = prompt.slice(0, 300);
      currentUserPrompt = truncated;
      extraArgs.push("--user-prompt", truncated);
    } else {
      // Fallback to git branch only if no prompt available
      const branch = getGitBranch(ctx && ctx.cwd);
      if (branch) {
        currentUserPrompt = branch;
        extraArgs.push("--user-prompt", branch);
      }
    }
    notify("active", "Thinking", extraArgs);
  });

  pi.on("agent_start", async (_event, ctx) => {
    // Prompt not available here - just signal working status.
    // user_prompt already captured in before_agent_start (set-once server-side).
    const extraArgs = [];
    const sessionID = safeString(ctx && ctx.sessionId);
    if (sessionID) {
      extraArgs.push("--agent-session-id", sessionID);
    }
    if (currentUserPrompt) {
      extraArgs.push("--user-prompt", currentUserPrompt);
    }
    notify("active", "Working", extraArgs);
  });

  pi.on("tool_execution_start", async (event, _ctx) => {
    // tool_execution_start fires before tool_call for every tool, so this
    // single handler covers activity/waiting labelling. Pi has no permission
    // popups, so there is no separate confirmation event to hook.
    const data = getEvent(event);
    notifyWithToolName(getToolName(data));
    const toolName = safeString(data.toolName || data.tool_name).toLowerCase();
    if (toolName === "write" || toolName === "edit" || toolName === "multiedit") {
      const path = safeString(data.args?.path || data.input?.path).trim();
      const toolCallId = safeString(data.toolCallId).trim();
      if (path && toolCallId) pendingPaths.set(toolCallId, path);
    }
  });

  pi.on("tool_execution_end", async (event, _ctx) => {
    const data = getEvent(event);
    const toolCallId = safeString(data.toolCallId).trim();
    if (!toolCallId) return;
    const path = pendingPaths.get(toolCallId);
    pendingPaths.delete(toolCallId);
    if (!path || data.isError) return;
    fireStdin(JSON.stringify({ hook_event_name: "PostToolUse", tool_name: "write", tool_input: { path } }));
  });

  pi.on("agent_end", async (event, _ctx) => {
    // Extract the last assistant text message so the sidebar has an agent
    // message (and hook history) for the completed turn.
    let agentMsg = "";
    try {
      const messages = getEvent(event).messages;
      if (Array.isArray(messages)) {
        for (let i = messages.length - 1; i >= 0 && !agentMsg; i--) {
          const msg = messages[i] || {};
          if (msg.role !== "assistant") continue;
          const content = msg.content;
          if (typeof content === "string") {
            agentMsg = content;
          } else if (Array.isArray(content)) {
            for (const block of content) {
              if (block && block.type === "text" && typeof block.text === "string" && block.text) {
                agentMsg = block.text;
                break;
              }
            }
          }
        }
      }
    } catch (e) {
      // Never crash Pi
    }
    const truncated = safeString(agentMsg).slice(0, 300);
    const extraArgs = truncated ? ["--agent-message", truncated] : [];
    notify("completed", truncated || "Task complete", extraArgs);
    currentUserPrompt = "";
  });
}
