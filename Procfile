# Procfile — managed by Overmind
# Start with: overmind start  (or: mise run dev)
#
# Each line has the form:  name: command
# Overmind opens a tmux window per process and restarts on failure.

# copilot: LLM proxy (copilot-proxy-go) — serves OpenAI-compatible API at 127.0.0.1:4141
copilot: copilot-gpt4-service

# app: hot-reload the Go service using air
app: air -c .air.toml
