# Procfile — managed by Overmind
# Start with: overmind start  (or: mise run dev)
#
# Each line has the form:  name: command
# Overmind opens a tmux window per process and restarts on failure.

# app: hot-reload the Go service using air
app: air -c .air.toml

# tencentdb: TencentDB Agent Memory gateway (Node.js sidecar on :8420)
# Install first with: mise run tencentdb:install
tencentdb: sh -c 'cd "$HOME/.memory-tencentdb/tdai-memory-openclaw-plugin" && exec npx tsx src/gateway/server.ts'
