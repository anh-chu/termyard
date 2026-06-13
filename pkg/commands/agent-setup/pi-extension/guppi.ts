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

  // Capture user prompt BEFORE agent starts (prompt field only exists here)
  let currentTask = "";
  let currentUserPrompt = "";
  
  pi.on("before_agent_start", async (event, ctx) => {
    const extraArgs = [];
    // Task = first user prompt (primary), git branch (fallback only)
    const data = getEvent(event);
    const prompt = safeString(data.prompt);
    if (prompt) {
      const truncated = prompt.slice(0, 300);
      currentTask = truncated.slice(0, 60);
      currentUserPrompt = truncated;
      extraArgs.push("--user-prompt", truncated);
      extraArgs.push("--task", currentTask);
    } else {
      // Fallback to git branch only if no prompt available
      const branch = getGitBranch(ctx && ctx.cwd);
      if (branch) {
        currentTask = branch;
        extraArgs.push("--task", branch);
      }
    }
    notify("active", "Thinking", extraArgs);
  });

  pi.on("agent_start", async (_event, _ctx) => {
    // Prompt not available here - just signal working status
    // Task/user-prompt already set in before_agent_start
    const extraArgs = [];
    if (currentTask) {
      extraArgs.push("--task", currentTask);
    }
    if (currentUserPrompt) {
      extraArgs.push("--user-prompt", currentUserPrompt);
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

  pi.on("agent_end", async (_event, _ctx) => {
    // Let terminal capture handle the last message
    notify("completed", "Task complete");
  });
}
