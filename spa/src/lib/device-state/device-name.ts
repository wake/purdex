// spa/src/lib/device-state/device-name.ts — moved to `lib/device-name.ts`, which
// survives this module's removal (Profile Sync P4b). Re-exported for the
// device-state callers that remain until then.
export { parseUserAgentName, resolveDefaultDeviceName } from '../device-name'
