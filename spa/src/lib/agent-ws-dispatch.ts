// Compat shim — all dispatch logic lives in ./agent-ws/. Re-exported here so
// existing imports (`./agent-ws-dispatch`) continue to work; new code should
// import from `./agent-ws` directly.
export { dispatchAgentWsEvent } from './agent-ws'
