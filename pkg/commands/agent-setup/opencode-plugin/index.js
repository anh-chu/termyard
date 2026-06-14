const guppi = "__GUPPI_BIN__";
const sessions = new Map();

function sessionState(sessionID) {
  if (!sessions.has(sessionID)) {
    sessions.set(sessionID, { userPromptSet: false, messages: {} });
  }
  return sessions.get(sessionID);
}

function compactText(value, maxLen = 240) {
  if (typeof value !== 'string') return '';
  const text = value.replace(/\s+/g, ' ').trim();
  if (!text) return '';
  return text.length > maxLen ? `${text.slice(0, maxLen - 1)}…` : text;
}

// fire sends args to guppi notify with no stdin.
function fire(args) {
  try {
    const proc = Bun.spawn([guppi, 'notify', ...args], {
      stdin: 'ignore',
      stdout: 'ignore',
      stderr: 'ignore',
    });
    void proc.exited.catch(() => {});
  } catch {}
}

// fireStdin sends a JSON payload to guppi notify via stdin.
// This lets the Go side do activity-label mapping (toolNameToActivity),
// keeping the mapping in one place rather than duplicating it here.
function fireStdin(payload) {
  try {
    const proc = Bun.spawn([guppi, 'notify', '-t', 'opencode', '--stdin'], {
      stdin: Buffer.from(payload),
      stdout: 'ignore',
      stderr: 'ignore',
    });
    void proc.exited.catch(() => {});
  } catch {}
}

function notify(status, message, extraArgs = []) {
  fire(['-t', 'opencode', '-s', status, '-m', message, ...extraArgs]);
}

export default {
  id: 'guppi',
  server: async function GuppiPlugin() {
    return {
      'permission.ask': async () => {
        notify('waiting', 'Permission needed');
      },
      'command.execute.before': async () => {
        fireStdin(JSON.stringify({ hook_event_name: 'PreToolUse', tool_name: 'bash' }));
      },
      'tool.execute.before': async ({ tool }) => {
        fireStdin(JSON.stringify({ hook_event_name: 'PreToolUse', tool_name: tool }));
      },
      'tool.execute.after': async ({ tool }) => {
        fireStdin(JSON.stringify({ hook_event_name: 'PostToolUse', tool_name: tool }));
      },
      event: async ({ event }) => {
        if (!event || !event.type) return;
        const props = event.properties;
        if (!props) return;
        const sessionID = props.sessionID;
        if (!sessionID) return;

        // Track message roles: message.updated carries {info.id, info.role}
        if (event.type === 'message.updated') {
          const info = props.info;
          if (info?.id && info?.role) {
            const state = sessionState(sessionID);
            state.messages[info.id] = info.role;
          }
          return;
        }

        // message.part.updated with type=text carries the actual text content
        if (event.type === 'message.part.updated') {
          const part = props.part;
          if (!part || part.type !== 'text' || !part.text || !part.messageID) return;

          const state = sessionState(sessionID);
          const role = state.messages[part.messageID];

          if (role === 'user') {
            if (state.userPromptSet) return;
            state.userPromptSet = true;
            const text = compactText(part.text);
            if (text) notify('active', 'Thinking', ['--task', text, '--user-prompt', text]);
          } else if (role === 'assistant') {
            const text = compactText(part.text);
            if (text) notify('active', 'Working', ['--agent-message', text]);
          }
          return;
        }

        if (event.type === 'session.idle') {
          notify('completed', 'Task complete');
          return;
        }

        if (event.type === 'session.error') {
          notify('error', 'Error');
        }
      },
    };
  },
};
