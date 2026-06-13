export default function (pi) {
  const guppiBin = process.env.GUPPI_BIN || "__GUPPI_BIN__";
  const { spawnSync } = require("child_process");

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
      spawnSync(guppiBin, ["notify", "-t", "pi", "-s", status, "-m", message, ...extraArgs], { stdio: "ignore" });
    } catch (e) {
      // Never crash Pi
    }
  };

  // Pass tool name via stdin JSON so guppi maps it to an activity label
  // (e.g. "bash" → "running commands", "read" → "reading files")
  const notifyWithToolName = (toolName) => {
    try {
      const payload = JSON.stringify({ hook_event_name: "PreToolUse", tool_name: safeString(toolName) });
      spawnSync(guppiBin, ["notify", "-t", "pi", "--stdin"], {
        input: payload,
        stdio: ["pipe", "ignore", "ignore"],
        encoding: "utf8",
      });
    } catch (e) {
      // Never crash Pi
    }
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

  pi.on("agent_start", async (event, ctx) => {
    const extraArgs = [];
    // Task = first user prompt (primary), git branch (fallback only)
    const data = getEvent(event);
    const prompt = safeString(data.prompt);
    if (prompt) {
      const truncated = prompt.slice(0, 300);
      extraArgs.push("--user-prompt", truncated);
      extraArgs.push("--task", truncated.slice(0, 60));
    } else {
      // Fallback to git branch only if no prompt available
      const branch = getGitBranch(ctx && ctx.cwd);
      if (branch) {
        extraArgs.push("--task", branch);
      }
    }
    notify("active", "Working", extraArgs);
  });

  pi.on("tool_execution_start", async (event, _ctx) => {
    const data = getEvent(event);
    notifyWithToolName(getToolName(data));
  });

  pi.on("tool_call", async (event, _ctx) => {
    const data = getEvent(event);
    const toolName = getToolName(data);
    if (data.requiresConfirmation) {
      notify("waiting", "Permission needed");
    } else {
      notifyWithToolName(toolName);
    }
  });

  pi.on("agent_end", async (event, _ctx) => {
    // Extract the last assistant message with actual text content
    let agentMsg = "";
    try {
      const data = getEvent(event);
      const messages = data.messages;
      if (Array.isArray(messages)) {
        // Iterate backwards to find the last assistant message with text
        for (let i = messages.length - 1; i >= 0; i--) {
          const msg = messages[i] || {};
          if (msg.role === "assistant") {
            const content = msg.content;
            let foundText = "";
            
            if (typeof content === "string") {
              foundText = content;
            } else if (Array.isArray(content)) {
              // Look for text blocks (skip toolCall blocks)
              for (const block of content) {
                if (block && block.type === "text" && typeof block.text === "string") {
                  foundText = block.text;
                  break;
                }
              }
            }
            
            // Only use this message if we found actual text
            if (foundText) {
              agentMsg = foundText;
              break;
            }
            // Otherwise continue to the previous message
          }
        }
      }
    } catch (e) {
      // Never crash Pi
    }
    const truncated = safeString(agentMsg).slice(0, 300);
    const extraArgs = truncated ? ["--agent-message", truncated] : [];
    notify("completed", truncated || "Task complete", extraArgs);
  });
}
